package cronboot_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/cronboot"
	"github.com/bpcoder16/baize/bootstrap/redisboot"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/infra/redisx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func writeConfig(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledCron(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "app.yaml", "env: {appName: test, runMode: test, timeLocation: UTC}\nlog: {format: text}\ndisabledServices: [cron]\n")
	plan := bootstrap.Plan{Services: []bootstrap.Service{cronboot.Service()}}
	if err := bootstrap.Run(t.Context(), filepath.Join(dir, "app.yaml"), plan); !errors.Is(err, apprun.ErrNoServices) {
		t.Fatalf("禁用 Cron 且没有 cron.yaml 时得到 %v", err)
	}
}

func TestCronService(t *testing.T) {
	// 环境和 Redis 注册表在进程内只能初始化一次。
	if os.Getenv("BAIZE_TEST_CRON") == "" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCronService$")
		cmd.Env = append(os.Environ(), "BAIZE_TEST_CRON=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	if err := env.Init(env.Config{AppName: "test", RunMode: "test", TimeLocation: "UTC", ConfigDirPath: t.TempDir(), LocalIP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	t.Run("配置与绑定", testBinding)
	t.Run("Redis锁与排空", testLockAndDrain)
}

func testBinding(t *testing.T) {
	dir := t.TempDir()
	job := cronx.NamedHandler{Name: "Heartbeat", Handler: func(context.Context) error { return nil }}
	handlers := []cronx.NamedHandler{job}
	decl := cronboot.Service(handlers...)
	handlers[0].Name = "changed"
	if decl.Name != "cron" {
		t.Fatal(decl.Name)
	}
	if _, err := decl.Build(t.Context(), dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺失 cron.yaml 时得到 %v", err)
	}
	for _, body := range []string{
		"jobz: []",
		"jobs: [{name: heartbeat, every: 1s}]",
		"jobs: [{name: Heartbeat, every: 1s}, {name: Heartbeat, every: 2s}]",
		"jobs: [{name: Heartbeat, every: 0s, cron: '* * * * * *'}]",
		"jobs: [{name: Heartbeat, randomEvery: {min: 1s, max: 500ms}}]",
		"jobs: [{name: Heartbeat, every: 1s, successLevel: warn}]",
		"lock: {}",
		"lock: {redisName: default, keyPrefix: 'test:', ttl: 3s, renewInterval: 3s}",
		"lock: {redisName: missing, keyPrefix: 'test:'}",
	} {
		writeConfig(t, dir, "cron.yaml", body)
		if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "cron.yaml") {
			t.Fatalf("配置 %s 得到 %v", body, err)
		}
	}
	writeConfig(t, dir, "cron.yaml", "jobs: []")
	for _, invalid := range [][]cronx.NamedHandler{{job, job}, {{Name: "empty"}}, {{Name: " space", Handler: job.Handler}}} {
		if _, err := cronboot.Service(invalid...).Build(t.Context(), dir); err == nil {
			t.Fatal("接受了非法 Handler 声明")
		}
	}
	for _, body := range []string{
		"jobs: []",
		"jobs: [{name: Heartbeat, every: 1m, successLevel: info}]",
		"jobs: [{name: Heartbeat, cron: '0 * * * * *'}]",
		"jobs: [{name: Heartbeat, randomEvery: {min: 1s, max: 2s}, successLevel: debug}]",
	} {
		writeConfig(t, dir, "cron.yaml", body)
		s, err := decl.Build(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := s.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := s.Run(); err != nil {
			t.Fatalf("停机后才进入 Run 得到 %v", err)
		}
	}
}

func testLockAndDrain(t *testing.T) {
	server := miniredis.RunT(t)
	dir := t.TempDir()
	writeConfig(t, dir, "redis.jobs.yaml", fmt.Sprintf("host: %s\nport: %s\nmaxRetries: -1\n", server.Host(), server.Port()))
	var resources lifecycle.Stack
	defer resources.Close()
	if err := redisboot.Instances("jobs").Init(dir, &resources); err != nil {
		t.Fatal(err)
	}
	const lock = "lock: {redisName: jobs, keyPrefix: 'test:', ttl: 15s, renewInterval: 5s}\n"
	const key = "test:Heartbeat"
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	decl := cronboot.Service(cronx.NamedHandler{Name: "Heartbeat", Handler: func(ctx context.Context) error {
		entered <- ctx
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	}})
	writeConfig(t, dir, "cron.yaml", lock+"jobs: [{name: Heartbeat, every: 100ms}]")
	if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("带锁任务缺少 timeout 时得到 %v", err)
	}
	writeConfig(t, dir, "cron.yaml", lock+"jobs: [{name: Heartbeat, every: 100ms, timeout: 10s}]")
	s, err := decl.Build(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.Cancel()
		if err := s.Wait(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if err := server.Set(key, "other-worker"); err != nil {
		t.Fatal(err)
	}
	before := server.CommandCount()
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	// 等待调度器实际尝试获取已占用的锁，再让下一轮获得它。
	deadline := time.Now().Add(3 * time.Second)
	for server.CommandCount() == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.CommandCount() == before {
		t.Fatal("调度器未尝试加锁")
	}
	select {
	case <-entered:
		t.Fatal("其他实例持锁时执行了任务")
	default:
	}
	server.Del(key)
	var jobCtx context.Context
	select {
	case jobCtx = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("释放竞争锁后任务未触发")
	}
	if ttl := server.TTL(key); ttl != 15*time.Second {
		t.Fatalf("任务持锁租期 %v", ttl)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Wait(waitCtx); !errors.Is(err, context.Canceled) || jobCtx.Err() != nil {
		t.Fatalf("任务执行中应继续排空，Wait=%v，任务状态=%v", err, jobCtx.Err())
	}
	close(release)
	if err := s.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || server.Exists(key) {
		t.Fatalf("排空后 Run=%v，锁仍存在=%t", err, server.Exists(key))
	}
	if err := redisx.Named("jobs").Client().Ping(t.Context()).Err(); err != nil {
		t.Fatalf("服务关闭后共享 Redis 无法使用: %v", err)
	}
}
