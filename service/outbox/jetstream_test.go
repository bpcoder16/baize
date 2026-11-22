package outbox

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
)

func TestJetStreamTaskSnapshotAndRequiredFields(t *testing.T) {
	msg := JetStreamMessage{
		Subject:   "orders.created",
		Data:      []byte{},
		Header:    map[string][]string{},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	names := []string{"events", "audit"}
	for _, name := range []string{"", "audit"} {
		msg.ResourceName = name
		task, err := msg.task(names)
		want := name
		if want == "" {
			want = "events"
		}
		if err != nil || task.HandlerType != constant.NatsJetStream || task.ResourceName != want || task.ExpiresAt != msg.ExpiresAt {
			t.Fatalf("目标或期限错误: %+v %v", task, err)
		}
		var payload model.JetStreamPayload
		if err := json.Unmarshal(task.Payload, &payload); err != nil || payload.Subject != msg.Subject || payload.Header == nil || payload.Data == nil {
			t.Fatalf("消息快照错误: %+v %v", payload, err)
		}
	}
	for name, mutate := range map[string]func(*JetStreamMessage){
		"subject":     func(m *JetStreamMessage) { m.Subject = "orders.*" },
		"empty-token": func(m *JetStreamMessage) { m.Subject = "orders..created" },
		"data":        func(m *JetStreamMessage) { m.Data = nil },
		"header":      func(m *JetStreamMessage) { m.Header = nil },
		"expires_at":  func(m *JetStreamMessage) { m.ExpiresAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := msg
			mutate(&invalid)
			if _, err := invalid.task(names); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("非法入队参数: %v", err)
			}
		})
	}
	msg.ResourceName = "unknown"
	if _, err := msg.task(names); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("未声明的目标: %v", err)
	}
}

func TestJetStreamTaskValidatesHeaderNames(t *testing.T) {
	msg := JetStreamMessage{
		Subject:   "orders.created",
		Data:      []byte{},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	names := []string{"events"}
	for _, key := range []string{"", "X:Bad", "X/Bad", "X Bad", "X\x7f", "中文"} {
		msg.Header = map[string][]string{key: {"value"}}
		if _, err := msg.task(names); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("应在入队时拒绝 Header 名 %q: %v", key, err)
		}
	}
	msg.Header = map[string][]string{"X-Trace!_~": {"含换行\r\n的值"}}
	if _, err := msg.task(names); err != nil {
		t.Fatalf("合法的 Header 名和可由 SDK 规范化的值应当通过: %v", err)
	}
}
