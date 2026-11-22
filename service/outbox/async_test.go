package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/baize/service/outbox"
	"github.com/bpcoder16/pixiu/logit"
)

func TestDefaultSendAsyncTimeoutAndErrors(t *testing.T) {
	var output bytes.Buffer
	logger := logit.MustNew(
		logit.OptEncoder(logit.DefaultJSONEncoder),
		logit.OptWriter(logit.NewWriter(&output)),
	)
	previous := logit.Default()
	logit.SetDefault(logger)
	t.Cleanup(func() {
		logit.SetDefault(previous)
		_ = logit.Close(logger)
	})
	var contexts []context.Context
	store := storeWithBeginError(t, func(ctx context.Context) error {
		contexts = append(contexts, ctx)
		<-ctx.Done()
		return ctx.Err()
	})
	worker := newWorker(t, store, func(context.Context, model.Delivery) error {
		t.Error("数据库失败不应进入 Handler")
		return nil
	})
	unregister, err := outbox.Register(worker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unregister() })
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		outbox.SendAsyncJetStream(17)
		synctest.Wait()
		outbox.SendAsync(constant.NatsJetStream, 23)
		synctest.Wait()
		if time.Now() != start || len(contexts) != 2 {
			t.Fatalf("异步入口应立即返回并各执行一次: elapsed=%s calls=%d", time.Since(start), len(contexts))
		}
		if contexts[0] == contexts[1] {
			t.Fatal("不同任务不得共用执行 context")
		}
		time.Sleep(5*time.Second - time.Nanosecond)
		for _, ctx := range contexts {
			if err := ctx.Err(); err != nil {
				t.Fatalf("异步发送在 5 秒前被取消: %v", err)
			}
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
		if len(contexts) != 2 || len(lines) != 2 {
			t.Fatalf("每个异步调用应只尝试一次并记录失败: calls=%d logs=%s", len(contexts), output.String())
		}
		ids := map[int64]bool{17: true, 23: true}
		for _, line := range lines {
			var record struct {
				Message string `json:"msg"`
				TaskID  int64  `json:"task_id"`
				Error   string `json:"err"`
			}
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if record.Message != "OutboxAsyncFailed" || !ids[record.TaskID] || !strings.Contains(record.Error, context.DeadlineExceeded.Error()) {
				t.Fatalf("异步失败日志缺少任务标识或错误: %s", line)
			}
			delete(ids, record.TaskID)
		}
	})
}
