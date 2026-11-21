package bootstrap_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// 环境和日志是进程级资源，每个场景使用独立进程。
func TestRunPlan(t *testing.T) {
	mode := os.Getenv("BAIZE_TEST_PLAN")
	if mode == "" {
		for _, mode := range []string{"once", "disabled", "serve", "cancel-resource-group", "prepare-error", "build-error", "invalid-service", "unexpected-exit", "timeout"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunPlan$")
				cmd.Env = append(os.Environ(), "BAIZE_TEST_PLAN="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	body := "env: {appName: test, runMode: debug, timeLocation: UTC, localIP: 127.0.0.1}\nlog: {format: text, hookStd: false}\ndisabledServices: [http, 'nats-consumer:off']\nresourceCloseTimeout: 2s\n"
	if mode == "timeout" {
		body += "serviceShutdownTimeout: 50ms\n"
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := errors.New("test failure")
	closeFailure := errors.New("test close failure")
	stopped := make(chan struct{})
	service := bootstrap.Service{Name: "nats-consumer:on", Build: func(_ context.Context, configDir string) (apprun.Service, error) {
		if configDir != dir {
			t.Fatal(configDir)
		}
		record("build")
		s := apprun.Service{
			Run: func() error {
				record("run")
				if mode == "unexpected-exit" {
					return nil
				}
				cancel()
				<-stopped
				return nil
			},
			Stop: func() error {
				record("stop")
				close(stopped)
				return nil
			},
			Wait: func(ctx context.Context) error {
				record("wait")
				if mode == "timeout" {
					<-ctx.Done()
					return ctx.Err()
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 20*time.Second || time.Until(deadline) > 30*time.Second {
					t.Errorf("默认排空期限 = %v", deadline)
				}
				return nil
			},
			Cancel: func() { record("cancel") },
		}
		if mode == "invalid-service" {
			s.Run = nil
		}
		return s, nil
	}}
	disabled := func(context.Context, string) (apprun.Service, error) {
		t.Fatal("构造了已禁用服务")
		return apprun.Service{}, nil
	}
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{{Name: "pool", Init: func(_ string, stack *lifecycle.Stack) error {
			record("resource")
			return stack.Register(func() error {
				if mode == "timeout" {
					time.Sleep(100 * time.Millisecond)
				}
				record("close")
				if mode == "build-error" {
					return closeFailure
				}
				return nil
			})
		}}},
		Prepare: []bootstrap.Step{{Name: "migrate", Init: func(_ context.Context, _ string, _ *lifecycle.Stack) error {
			record("prepare")
			if mode == "prepare-error" {
				return failure
			}
			return nil
		}}},
		Services: []bootstrap.Service{{Name: "http:admin", Build: disabled}, {Name: "nats-consumer:off", Build: disabled}, service},
	}
	var wantErr error
	want := []string{"resource", "prepare"}
	switch mode {
	case "once":
		plan.Services = nil
	case "disabled":
		plan.Services = plan.Services[:2]
		wantErr = apprun.ErrNoServices
		want = nil
	case "cancel-resource-group":
		names := []string{"orders", "audit"}
		for _, name := range names {
			if err := os.WriteFile(filepath.Join(dir, "sample."+name+".yaml"), []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		group := bootstrap.NamedInstances("sample", names, func(name string, _ *struct{}) error {
			record("open " + name)
			cancel()
			return nil
		}, func() error {
			record("close sample")
			return nil
		})
		plan.Resources = append(plan.Resources, group, bootstrap.Resource{
			Name: "after-sample",
			Init: func(string, *lifecycle.Stack) error {
				return errors.New("取消后仍执行了下一项资源初始化")
			},
		})
		wantErr = context.Canceled
		want = []string{"resource", "open orders", "open audit", "close sample"}
	case "prepare-error":
		wantErr = failure
	case "build-error":
		plan.Services = append(plan.Services, bootstrap.Service{Name: "cron", Build: func(context.Context, string) (apprun.Service, error) {
			return apprun.Service{}, failure
		}})
		wantErr = failure
		want = append(want, "build", "stop", "wait")
	case "invalid-service":
		wantErr = apprun.ErrInvalidService
		want = append(want, "build", "stop", "wait")
	default:
		want = append(want, "build", "run", "stop", "wait")
		if mode == "unexpected-exit" {
			wantErr = apprun.ErrUnexpectedExit
		}
		if mode == "timeout" {
			wantErr = apprun.ErrShutdownTimeout
			want = append(want, "cancel")
		}
	}
	if mode != "disabled" {
		want = append(want, "close")
	}
	err := bootstrap.Run(ctx, path, plan)
	if mode == "build-error" && (!errors.Is(err, closeFailure) || !strings.Contains(err.Error(), "cron")) {
		t.Fatalf("构造错误缺少步骤名或回收错误: %v", err)
	}
	if !errors.Is(err, wantErr) || errors.Is(err, lifecycle.ErrCloseTimeout) {
		t.Fatalf("Run = %v, want %v", err, wantErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRunChecksDeclarationsBeforeInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("disabledServices: ['worker:events']"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := bootstrap.Service{Name: "worker:events", Build: func(context.Context, string) (apprun.Service, error) {
		t.Fatal("构造了配置校验失败的服务")
		return apprun.Service{}, nil
	}}
	plan := bootstrap.Plan{Services: []bootstrap.Service{worker}}
	if err := bootstrap.Run(t.Context(), path, plan); err == nil || !strings.Contains(err.Error(), "unknown service type") {
		t.Fatalf("应拒绝白名单外的服务禁用规则: %v", err)
	}
	plan.Services = append(plan.Services, worker)
	if err := bootstrap.Run(t.Context(), path, plan); err == nil || !strings.Contains(err.Error(), "duplicate service") {
		t.Fatalf("服务重名: %v", err)
	}
}
