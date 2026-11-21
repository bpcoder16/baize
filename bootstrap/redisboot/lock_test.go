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
	"github.com/bpcoder16/pixiu/biz/lockx"
	"github.com/bpcoder16/pixiu/biz/lockx/redislock"
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
	var resources lifecycle.Stack
	defer resources.Close()
	cfg := redislock.Config{TTL: 12 * time.Second}
	step := PrepareLock("orders", "jobs", cfg)
	named := PrepareLock("default", "jobs", cfg)
	if step.Name != "redis-orders-lock" || named.Name != "redis-default-lock" {
		t.Fatalf("步骤名应由执行器名称生成，实际为 %q 和 %q", step.Name, named.Name)
	}
	if err := step.Init(t.Context(), dir, &resources); err == nil || !strings.Contains(err.Error(), "jobs") {
		t.Fatalf("缺少 Redis 实例时应报告实例名，实际为 %v", err)
	}

	server := miniredis.RunT(t)
	for name, retries := range map[string]int{"jobs": -1, "cache": 0} {
		body := fmt.Sprintf("host: %s\nport: %s\nmaxRetries: %d\n", server.Host(), server.Port(), retries)
		if err := os.WriteFile(filepath.Join(dir, "redis."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Instances("jobs", "cache").Init(dir, &resources); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLock("cache-lock", "cache", cfg).Init(t.Context(), dir, &resources); err == nil || !strings.Contains(err.Error(), "MaxRetries") {
		t.Fatalf("开启命令重试时应拒绝装配，实际为 %v", err)
	}
	if err := named.Init(t.Context(), dir, &resources); err != nil {
		t.Fatal(err)
	}
	if err := step.Init(t.Context(), dir, &resources); err != nil {
		t.Fatal(err)
	}
	if err := step.Init(t.Context(), dir, &resources); err == nil {
		t.Fatal("重复注册同名执行器应返回错误")
	}

	const key = "demo:job:42"
	err := lockx.Named("orders").Do(t.Context(), key, func(ctx context.Context) error {
		if ttl := server.TTL(key); ttl != cfg.TTL {
			t.Errorf("锁的租期为 %s，期望 %s", ttl, cfg.TTL)
		}
		ran, err := lockx.Named("default").TryDo(ctx, key, func(context.Context) error {
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
