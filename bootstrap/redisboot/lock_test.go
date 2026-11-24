package redisboot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/service/lockx"
	"github.com/bpcoder16/baize/service/lockx/redislock"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestPrepareLock(t *testing.T) {
	// Redis 和锁执行器都使用进程级注册表。
	if os.Getenv("BAIZE_REDIS_LOCK_TEST") == "" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPrepareLock$")
		cmd.Env = append(os.Environ(), "BAIZE_REDIS_LOCK_TEST=1")
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
	server := miniredis.RunT(t)
	for name, retries := range map[string]int{"jobs": -1, "cache": 0, "outside": -1} {
		body := fmt.Sprintf("host: %s\nport: %s\nmaxRetries: %d\n", server.Host(), server.Port(), retries)
		if err := os.WriteFile(filepath.Join(dir, "redis."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var external lifecycle.Stack
	defer external.Close()
	if err := Instances("outside").Init(dir, &external); err != nil {
		t.Fatal(err)
	}
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{Instances("jobs", "cache")},
		Prepare: []bootstrap.Step{{
			Name: "check-locks",
			Init: func(ctx context.Context, dir string, resources *lifecycle.Stack) error {
				testPrepareLock(t, ctx, dir, resources, server)
				return nil
			},
		}},
	}
	if err := bootstrap.Run(t.Context(), appPath, plan); err != nil {
		t.Fatal(err)
	}
}

func testPrepareLock(t *testing.T, ctx context.Context, dir string, resources *lifecycle.Stack, server *miniredis.Miniredis) {
	t.Helper()
	cfg := redislock.Config{TTL: 12 * time.Second}
	step := PrepareLock("orders", "jobs", cfg)
	named := PrepareLock("default", "jobs", cfg)
	if step.Name != "baize.redis-orders-lock" || named.Name != "baize.redis-default-lock" {
		t.Fatalf("步骤名应由执行器名称生成，实际为 %q 和 %q", step.Name, named.Name)
	}
	if err := PrepareLock("outside-lock", "outside", cfg).Init(ctx, dir, resources); err == nil || !strings.Contains(err.Error(), `redis instance "outside" is not declared`) {
		t.Fatalf("应拒绝已注册但本次未声明的 Redis，实际为 %v", err)
	}
	if err := PrepareLock("cache-lock", "cache", cfg).Init(ctx, dir, resources); err == nil || !strings.Contains(err.Error(), "MaxRetries") {
		t.Fatalf("开启命令重试时应拒绝装配，实际为 %v", err)
	}
	if err := named.Init(ctx, dir, resources); err != nil {
		t.Fatal(err)
	}
	if err := step.Init(ctx, dir, resources); err != nil {
		t.Fatal(err)
	}
	if err := step.Init(ctx, dir, resources); err == nil {
		t.Fatal("重复注册同名执行器应返回错误")
	}

	const key = "demo:job:42"
	err := lockx.Named("orders").Do(ctx, key, func(lockCtx context.Context) error {
		if ttl := server.TTL(key); ttl != cfg.TTL {
			t.Errorf("锁的租期为 %s，期望 %s", ttl, cfg.TTL)
		}
		ran, err := lockx.Named("default").TryDo(lockCtx, key, func(context.Context) error {
			t.Error("同一个 key 的竞争者执行了回调")
			return nil
		})
		if ran {
			t.Error("持锁期间重复取得了锁")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Exists(key) {
		t.Fatal("回调结束后应释放锁")
	}
}
