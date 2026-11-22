package nats

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// 命名注册表关闭后不能重新初始化，各用例通过独立进程隔离。
func isolatedNamedClients(t *testing.T) bool {
	t.Helper()
	const marker = "BAIZE_NATSOUTBOX_NAMED_TEST"
	if os.Getenv(marker) == t.Name() {
		return true
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), marker+"="+t.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("命名客户端独立进程测试失败: %v\n%s", err, output)
	}
	return false
}

func TestJetStreamAcknowledgementAndStableID(t *testing.T) {
	if !isolatedNamedClients(t) {
		return
	}
	s, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS 未启动")
	}
	client, err := natsx.NewNamed(natsx.Config{
		Name:      "events",
		URLs:      []string{s.ClientURL()},
		Namespace: "test1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = natsx.CloseAll() })
	second, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second.Start()
	t.Cleanup(func() { second.Shutdown(); second.WaitForShutdown() })
	if !second.ReadyForConnections(5 * time.Second) {
		t.Fatal("audit NATS 未启动")
	}
	auditClient, err := natsx.NewNamed(natsx.Config{
		Name:      "audit",
		URLs:      []string{second.ClientURL()},
		Namespace: "test1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.JetStream().CreateStream(ctx, jetstream.StreamConfig{
		Name:     "TEST1_EVENTS",
		Subjects: []string{"test1.events.created"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	auditStream, err := auditClient.JetStream().CreateStream(ctx, jetstream.StreamConfig{
		Name:     "TEST2_EVENTS",
		Subjects: []string{"test1.events.created"},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	clients := map[string]*natsx.Client{"events": client, "audit": auditClient}
	h := NewJetStreamHandler(clients)
	delete(clients, "events")
	data, _ := json.Marshal(map[string]any{
		"subject": "events.created",
		"data":    []byte{0, 1, 255},
		"header": nats.Header{
			"X-Source":    []string{"a", "b"},
			"X-Lines":     []string{"a\r\nb"},
			"Nats-Msg-Id": []string{"old"},
		},
		"extra": true, // 未知字段不影响消息发布。
	})
	d := model.Delivery{
		ID:           42,
		ResourceName: "events",
		Payload:      data,
	}
	withoutResource := model.Delivery{
		ID:      43,
		Payload: []byte(`{"client":"events","subject":"events.created"}`),
	}
	if err := h.Publish(ctx, withoutResource); !errors.Is(err, outboxerr.ErrInvalidResource) {
		t.Fatalf("缺少 resource_name 的任务必须拒绝: %v", err)
	}
	for range 2 {
		if err := h.Publish(ctx, d); err != nil {
			t.Fatalf("未取得确认: %v", err)
		}
	}
	info, err := stream.Info(ctx)
	if err != nil || info.State.Msgs != 1 {
		t.Fatalf("稳定 ID 未去重: %+v %v", info, err)
	}

	msg, err := stream.GetMsg(ctx, 1)
	if err != nil || msg.Subject != "test1.events.created" || string(msg.Data) != string([]byte{0, 1, 255}) || msg.Header.Get("Nats-Msg-Id") != "42" || len(msg.Header.Values("X-Source")) != 2 {
		t.Fatalf("消息快照变化: %+v %v", msg, err)
	}
	if got := msg.Header.Get("X-Lines"); got != "a  b" {
		t.Fatalf("Header 换行应由 SDK 规范化: %q", got)
	}
	d.Payload = []byte(`{"subject":"events.created","header":{"X:Bad":["value"]}}`)
	if err := h.Publish(ctx, d); !errors.Is(err, nats.ErrBadHeaderMsg) {
		t.Fatalf("非法 Header 名应返回 SDK 错误: %v", err)
	}
	d.ResourceName = "audit"
	d.Payload = []byte(`{"subject":"events.created","data":"YXVkaXQ="}`)
	if err := h.Publish(ctx, d); err != nil {
		t.Fatalf("按 resource_name 发布失败: %v", err)
	}
	auditMsg, err := auditStream.GetMsg(ctx, 1)
	if err != nil || auditMsg.Subject != "test1.events.created" || string(auditMsg.Data) != "audit" || auditMsg.Header.Get("Nats-Msg-Id") != "42" {
		t.Fatalf("消息未发布到指定命名客户端: %+v %v", auditMsg, err)
	}
	if err := NewJetStreamHandler(map[string]*natsx.Client{"events": client}).Publish(ctx, d); err == nil {
		t.Fatal("已注册但未列入 Handler 的客户端不得发布")
	}
	for _, suffix := range []string{" {}", " invalid"} {
		d.Payload = []byte(string(data) + suffix)
		if err := h.Publish(ctx, d); err == nil {
			t.Fatalf("尾部额外数据未拒绝: %q", suffix)
		}
	}
	d.ResourceName = "missing"
	d.Payload = []byte(`{`)
	if err := h.Publish(ctx, d); !errors.Is(err, outboxerr.ErrInvalidResource) {
		t.Fatalf("无效目标应在解码正文前拒绝: %v", err)
	}
	if err := natsx.CloseAll(); err != nil {
		t.Fatal(err)
	}
	d.ResourceName = "events"
	d.Payload = data
	if err := h.Publish(ctx, d); !errors.Is(err, natsx.ErrClosed) {
		t.Fatalf("Handler 应持有构造时取得的客户端，不再查询注册表: %v", err)
	}
}
