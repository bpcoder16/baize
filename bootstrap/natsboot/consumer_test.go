package natsboot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestDisabledConsumerService(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	body := "env: {appName: test, runMode: test, timeLocation: UTC}\nlog: {format: text}\ndisabledServices: ['nats-consumer:events']\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := bootstrap.Plan{Services: []bootstrap.Service{ConsumerService("events", nil, nil)}}
	if err := bootstrap.Run(t.Context(), path, plan); !errors.Is(err, apprun.ErrNoServices) {
		t.Fatalf("禁用实例时应跳过读取订阅配置，得到 %v", err)
	}
}

func TestConsumerService(t *testing.T) {
	// 命名客户端关闭后不能重新登记，每次使用独立进程。
	if os.Getenv("BAIZE_CONSUMER_TEST") == "" {
		ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConsumerService$")
		cmd.Env = append(os.Environ(), "BAIZE_CONSUMER_TEST=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	dir := t.TempDir()
	t.Chdir(dir)
	appPath := filepath.Join(dir, "app.yaml")
	app := "env: {appName: test, runMode: test, timeLocation: UTC, localIP: 127.0.0.1}\nlog: {format: text, hookStd: false}\n"
	if err := os.WriteFile(appPath, []byte(app), 0o600); err != nil {
		t.Fatal(err)
	}
	var srv, stoppingSrv *server.Server
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{
			{
				Name: "test-nats-server",
				Init: func(dir string, _ *lifecycle.Stack) error {
					srv = startConsumerTestServer(t)
					// 停机测试单独断开重连客户端，其余连接随 Plan 正常排空。
					stoppingSrv = startConsumerTestServer(t)
					events := "urls: [" + srv.ClientURL() + "]\nnamespace: test\n"
					if err := os.WriteFile(filepath.Join(dir, "nats.events.yaml"), []byte(events), 0o600); err != nil {
						return err
					}
					stopping := "urls: [" + stoppingSrv.ClientURL() + "]\n"
					return os.WriteFile(filepath.Join(dir, "nats.stopping.yaml"), []byte(stopping), 0o600)
				},
			},
			Instances("events", "stopping"),
		},
		Prepare: []bootstrap.Step{{
			Name: "check-consumer",
			Init: func(ctx context.Context, dir string, _ *lifecycle.Stack) error {
				testConsumerService(t, ctx, dir, srv, stoppingSrv)
				return nil
			},
		}},
	}
	if err := bootstrap.Run(t.Context(), appPath, plan); err != nil {
		t.Fatal(err)
	}
}

func startConsumerTestServer(t *testing.T) *server.Server {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		JetStream: true, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	})
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("临时 NATS 未就绪")
	}
	return srv
}

func testConsumerService(t *testing.T, planCtx context.Context, dir string, srv, stoppingSrv *server.Server) {
	t.Helper()
	if _, err := natsx.NewNamed(natsx.Config{Name: "outside", URLs: []string{srv.ClientURL()}}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	js := &natsx.JetStreamHandlerRegistry{}
	js.MustRegister(func(ctx context.Context, msg jetstream.Msg) error {
		if string(msg.Data()) != "event" {
			return errors.New("unexpected event")
		}
		entered <- ctx
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	}, "Event")
	core := &natsx.CoreHandlerRegistry{}
	core.MustRegister(func(_ context.Context, msg *nats.Msg) error { return msg.Respond([]byte("pong")) }, "Ping")
	decl := ConsumerService("events", js, core)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const filename = "nats-register.events.yaml"
	write("nats-register.outside.yaml", "{}")
	if svc, err := ConsumerService("outside", js, core).Build(planCtx, dir); err == nil {
		svc.Cancel()
		t.Fatal("已注册但本次未声明的 NATS 不应构造消费服务")
	} else if !strings.Contains(err.Error(), `nats instance "outside" is not declared`) {
		t.Fatalf("应报告未声明的 NATS，得到 %v", err)
	}
	for _, tc := range []struct{ body, want string }{
		{"jetStreamConsumers: []", "jetstreamconsumers"},
		{"coreConsumers: [{name: x, subject: x, handler: ping}]", "ping"},
		{"coreConsumers: [{name: x, subject: x, queue: q, subscribers: 0, handler: Ping}]", "subscribers"},
		{"streams: [{name: X, subjects: [x], consumers: [{durable: x, handler: Missing}]}]", "Missing"},
		{"streams: [{name: X, mode: existing, subjects: [x]}]", "existing"},
		{"streamDefaults: {replicas: 1}\nstreams: [{name: X, subjects: [x], replicas: 2}]", "replicas"},
	} {
		write(filename, tc.body)
		if _, err := decl.Build(planCtx, dir); err == nil || !strings.Contains(err.Error(), filename) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("配置 %q 应报告路径及 %q，得到 %v", tc.body, tc.want, err)
		}
	}
	// 后续声明的配置有误时，前面的合法 Stream upsert 也不能执行。
	client := natsx.Named("events")
	cfg := natsx.DefaultStreamConfig("TEST_CHECK", 1, "test.check")
	cfg.MaxBytes = 16384
	stream, streamErr := client.JetStream().CreateStream(t.Context(), cfg)
	if streamErr != nil {
		t.Fatal(streamErr)
	}
	original := stream.CachedInfo().Config
	const upsert = `streams:
  - name: CHECK
    mode: upsert
    subjects: [check]
    maxBytes: 8192
`
	const consumer = `    consumers:
      - durable: worker
        handler: Event
        backOff: [1s, 2s]
`
	for _, tc := range []struct {
		name   string
		suffix string
		want   []string
	}{
		{"RetryDelay", consumer + "        retryDelay: 1s\n", []string{"BackOff", "RetryDelay"}},
		{"MaxDeliver", consumer + "        maxDeliver: 1\n", []string{"BackOff", "MaxDeliver"}},
		{
			name: "default-duplicates",
			// 仅覆盖保留时长，沿用 DefaultStreamConfig 的 2 分钟去重窗口。
			suffix: `  - name: SHORT
    subjects: [short]
    maxAge: 30s
`,
			want: []string{"SHORT", "duplicates", "maxAge"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(filename, upsert+tc.suffix)
			_, buildErr := decl.Build(planCtx, dir)
			if buildErr == nil {
				t.Fatal("Build 应拒绝冲突配置")
			}
			for _, field := range tc.want {
				if !strings.Contains(buildErr.Error(), field) {
					t.Errorf("Build 错误应包含 %q，得到 %v", field, buildErr)
				}
			}
			info, err := stream.Info(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(info.Config, original) {
				t.Errorf("预检改变了已有 Stream 的配置：得到 %+v，期望 %+v", info.Config, original)
			}
		})
	}
	write(filename, `streams:
  - name: EVENTS
    subjects: ["demo.events.*"]
    consumers:
      - durable: worker
        filterSubjects: ["demo.events.created"]
        handler: Event
coreConsumers:
  - name: ping
    subject: demo.ping
    queue: workers
    handler: Ping
`)
	s, buildErr := decl.Build(planCtx, dir)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if s.Name != "nats-consumer:events" || srv.GlobalAccount().SubscriptionInterest("test.demo.ping") {
		t.Fatal("服务应按实例命名，并延迟至 Run 才订阅")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	defer func() {
		s.Cancel()
		if err := s.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()
	for !srv.GlobalAccount().SubscriptionInterest("test.demo.ping") {
		select {
		case err := <-done:
			t.Fatalf("注册失败: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	reply, requestErr := client.Request(ctx, "demo.ping", nil)
	if requestErr != nil || string(reply.Data) != "pong" {
		t.Fatalf("Core 请求应答失败: %v, %v", reply, requestErr)
	}
	if _, err := client.PublishJetStream(ctx, "demo.events.created", []byte("event"), "1"); err != nil {
		t.Fatal(err)
	}
	var handlerCtx context.Context
	select {
	case handlerCtx = <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	expired, stopWait := context.WithCancel(ctx)
	stopWait()
	if err := s.Wait(expired); !errors.Is(err, context.Canceled) || handlerCtx.Err() != nil {
		t.Fatalf("停机应等待在途 Handler，Wait=%v，Handler=%v", err, handlerCtx.Err())
	}
	close(release)
	if err := s.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := client.Publish(ctx, "after.stop", nil); err != nil {
		t.Fatalf("停收后共享客户端应仍可发布: %v", err)
	}
	t.Run("Core注册断连时停机", func(t *testing.T) {
		nc := natsx.Named("stopping")
		write("nats-register.stopping.yaml", "coreConsumers: [{name: ping, subject: p, queue: q, subscribers: 1, handler: Ping}]")
		service, err := ConsumerService("stopping", nil, core).Build(planCtx, dir)
		if err != nil {
			t.Fatal(err)
		}
		conn := nc.Conn()
		reconnecting := conn.StatusChanged(nats.RECONNECTING)
		defer conn.RemoveStatusListener(reconnecting)
		stoppingSrv.Shutdown()
		stoppingSrv.WaitForShutdown()
		select {
		case <-reconnecting:
		case <-time.After(3 * time.Second):
			t.Fatal("连接未进入重连状态")
		}

		baseline := conn.NumSubscriptions()
		done := make(chan error, 1)
		go func() { done <- service.Run() }()
		defer conn.Close()
		deadline := time.Now().Add(3 * time.Second)
		for conn.NumSubscriptions() == baseline && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if conn.NumSubscriptions() == baseline {
			t.Fatal("未进入 Core 注册阶段")
		}
		if stopErr := service.Stop(); stopErr != nil {
			t.Fatal(stopErr)
		}
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Fatalf("停机期间注册应正常退出：%v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("停机未中断 Core 注册等待")
		}
	})
}
