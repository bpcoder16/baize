package outbox_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/pixiu/logit"
	"gorm.io/gorm"
)

func TestSendAsyncFailureAndCronRecovery(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx, cancel := context.WithTimeout(logit.WithContextLogID(context.Background()), 10*time.Second)
		defer cancel()
		logID, _ := logit.LogIDFromContext(ctx)
		id := enqueueTask(t, s, db.WithContext(ctx), task())
		var calls atomic.Int32
		sendErr := errors.New("异步发送失败")
		worker := testWorker(t, s, func(ctx context.Context, _ model.Delivery) error {
			if got, _ := logit.LogIDFromContext(ctx); got != logID {
				t.Errorf("发送应恢复任务保存的 logId: got=%q want=%q", got, logID)
			}
			if calls.Add(1) == 1 {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
					t.Errorf("首次异步发送应有独立 5 秒期限: %s", deadline)
				}
				return sendErr
			}
			return nil
		})
		worker.SendAsync(id)
		// 等待已提交结果，不能只在 Handler 返回前就开始补偿。
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			record, err := s.TestReadRecord(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if record.Attempts == 1 {
				if record.Status != constant.OutboxPending || record.LastError != sendErr.Error() || calls.Load() != 1 {
					t.Fatalf("异步失败应保留 pending 且只尝试一次: %+v calls=%d", record, calls.Load())
				}
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("未保存异步发送结果")
			}
		}
		if err := worker.RetryPending(ctx, 10); err != nil {
			t.Fatal(err)
		}
		record, err := s.TestReadRecord(ctx, id)
		if err != nil || record.Status != constant.OutboxSucceeded || record.Attempts != 2 || record.LastError != "" || calls.Load() != 2 {
			t.Fatalf("cron 应补偿异步失败任务: %+v calls=%d err=%v", record, calls.Load(), err)
		}
	})
}
