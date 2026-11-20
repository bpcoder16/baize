package bootstrap_test

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

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/logit"
)

// 环境、时区和标准流均绑定进程，每个变体在独立进程中验证。
func TestInit(t *testing.T) {
	mode := os.Getenv("BAIZE_INIT_TEST")
	if mode == "" {
		for _, mode := range []string{"normal", "hook", "log-error"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInit$")
				cmd.Env = append(os.Environ(), "BAIZE_INIT_TEST="+mode)
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
	config := "env: {appName: cards, runMode: debug, timeLocation: Asia/Shanghai, localIP: 127.0.0.1}\ndisabledServices: [http]\nlog:\n  format: json\n  names: [audit]\n"
	if mode != "hook" {
		config += "  hookStd: false\n"
	}
	if mode == "log-error" {
		config += "  rotate: {every: 2h}\n"
	}
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	resources, err := bootstrap.Init(path)
	if mode == "log-error" {
		if resources != nil || err == nil || !strings.Contains(err.Error(), "log.rotate.every") {
			t.Fatalf("invalid rotation accepted: %v", err)
		}
		if _, retryErr := bootstrap.Init(path); !errors.Is(retryErr, env.ErrAlreadyInitialized) {
			t.Fatalf("日志预检失败后再次初始化: %v", retryErr)
		}
		if time.Local != env.TimeLocation() || time.Local.String() != "Asia/Shanghai" {
			t.Fatal("日志预检前应已应用配置的时区")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if env.ConfigDirPath() != dir || time.Local != env.TimeLocation() || time.Local.String() != "Asia/Shanghai" {
		t.Fatal("配置目录或时区错误")
	}
	if _, err := bootstrap.Init(path); !errors.Is(err, env.ErrAlreadyInitialized) {
		t.Fatalf("second init: %v", err)
	}
	if err := resources.Register(func() error {
		logit.Named("audit").Info(context.Background(), "closing resource")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "stdout probe")
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("log", "cards", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if !strings.Contains(read("biz/audit.info.log"), "closing resource") {
		t.Fatal("资源关闭期间日志未写入")
	}
	if mode == "hook" {
		if !strings.Contains(read("std/stdout.log"), "stdout probe") {
			t.Fatal("标准输出未重定向")
		}
	} else if _, err := os.Stat("log/cards/std"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hookStd=false: %v", err)
	}
}
