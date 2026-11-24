package redisboot

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/redisx"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/redis/go-redis/v9"
)

func TestFileConfig(t *testing.T) {
	cfg, err := configx.Parse[fileConfig](filepath.Join("..", "..", "conf.example", "redis.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Host, cfg.Port = "::1", 0
	got, err := cfg.clientConfig("cache")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "cache" || got.Options.Addr != "[::1]:6379" {
		t.Fatalf("实例名和默认地址 = %q %q", got.Name, got.Options.Addr)
	}
	if got.Options.MaxRetries != -1 || got.Options.DialTimeout != 3*time.Second ||
		got.Options.ReadTimeout != 3*time.Second || got.Options.WriteTimeout != 3*time.Second ||
		got.SlowThreshold != 200*time.Millisecond || !got.LogCommands {
		t.Fatal("模板中的重试、超时或日志配置未传给客户端")
	}
	if got.Options.PoolSize != 100 || got.Options.MinIdleConns != 10 || got.Options.MaxIdleConns != 20 ||
		got.Options.PoolTimeout != 4*time.Second || got.Options.ConnMaxIdleTime != 30*time.Minute {
		t.Fatal("连接池配置与模板不符")
	}
}

func TestInvalidFileConfig(t *testing.T) {
	for _, tc := range []struct {
		field string
		cfg   fileConfig
	}{
		{"host", fileConfig{Host: " localhost"}},
		{"port", fileConfig{Host: "localhost", Port: 65536}},
		{"db", fileConfig{Host: "localhost", DB: -1}},
		{"maxRetries", fileConfig{Host: "localhost", MaxRetries: -2}},
		{"readTimeout", fileConfig{Host: "localhost", ReadTimeout: -time.Second}},
		{"pool.timeout", fileConfig{Host: "localhost", Pool: poolConfig{Timeout: -time.Second}}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			_, err := tc.cfg.clientConfig("cache")
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("应指出无效配置项 %s，实际为 %v", tc.field, err)
			}
		})
	}
}

func TestInstances(t *testing.T) {
	// Pixiu 的命名注册表关闭后不能重开，各场景使用独立进程。
	mode := os.Getenv("BAIZE_REDIS_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "second-fails"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInstances$")
				cmd.Env = append(os.Environ(), "BAIZE_REDIS_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}

	server := miniredis.RunT(t)
	server.RequireUserAuth("demo", "test-password")
	dir := t.TempDir()
	for db, name := range []string{"default", "session"} {
		password := "test-password"
		if mode == "second-fails" && name == "session" {
			password = "wrong-password"
		}
		config := fmt.Sprintf("host: %s\nport: %s\nusername: demo\npassword: %s\ndb: %d\nmaxRetries: -1\n",
			server.Host(), server.Port(), password, db)
		if err := os.WriteFile(filepath.Join(dir, "redis."+name+".yaml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var resources lifecycle.Stack
	t.Cleanup(func() {
		if err := resources.Close(); err != nil {
			t.Error(err)
		}
	})
	initErr := Instances("default", "session").Init(dir, &resources)
	if mode == "second-fails" {
		if initErr == nil || !strings.Contains(initErr.Error(), "redis.session.yaml") {
			t.Fatalf("应在启动时报告第二个实例认证失败，实际为 %v", initErr)
		}
	} else {
		if initErr != nil {
			t.Fatal(initErr)
		}
		if err := redisx.Named("session").Client().Set(t.Context(), "example", "session", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if value, err := server.DB(1).Get("example"); err != nil || value != "session" {
			t.Fatalf("命名实例未使用指定数据库：%q, %v", value, err)
		}
	}
	client := redisx.Named("default").Client()
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("首个命名实例应可借用：%v", err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(t.Context()).Err(); !errors.Is(err, redis.ErrClosed) {
		t.Fatalf("资源栈未关闭已创建的 Redis 客户端：%v", err)
	}
}
