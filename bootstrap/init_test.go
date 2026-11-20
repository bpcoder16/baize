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
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/bpcoder16/pixiu/logit"
)

// 环境、时区和标准流均绑定进程，每个变体在独立进程中验证。
func TestRunInitialization(t *testing.T) {
	mode := os.Getenv("BAIZE_INIT_TEST")
	if mode == "" {
		for _, mode := range []string{"normal", "hook", "log-error", "close-timeout", "resource-error"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunInitialization$")
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
	if mode == "close-timeout" {
		config += "resourceCloseTimeout: 25ms\n"
	}
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if mode == "resource-error" {
		var closed []string
		failure := errors.New("resource failed")
		step := func(name string) bootstrap.Resource {
			return bootstrap.Resource{Name: name, Init: func(configDir string, stack *lifecycle.Stack) error {
				if configDir != dir {
					t.Fatalf("configDir = %q, want %q", configDir, dir)
				}
				if err := stack.Register(func() error {
					closed = append(closed, name)
					return nil
				}); err != nil {
					return err
				}
				if name == "second" {
					return failure
				}
				return nil
			}}
		}
		plan := bootstrap.Plan{Resources: []bootstrap.Resource{
			step("first"),
			step("second"),
			step("third"),
		}}
		err := bootstrap.Run(t.Context(), path, plan)
		if !errors.Is(err, failure) || strings.Join(closed, ",") != "second,first" {
			t.Fatalf("error = %v, closed = %v", err, closed)
		}
		return
	}
	if mode == "log-error" {
		plan := bootstrap.Plan{}
		err := bootstrap.Run(t.Context(), path, plan)
		if err == nil || !strings.Contains(err.Error(), "log.rotate.every") {
			t.Fatalf("invalid rotation accepted: %v", err)
		}
		if retryErr := bootstrap.Run(t.Context(), path, plan); !errors.Is(retryErr, env.ErrAlreadyInitialized) {
			t.Fatalf("日志预检失败后再次初始化: %v", retryErr)
		}
		if time.Local != env.TimeLocation() || time.Local.String() != "Asia/Shanghai" {
			t.Fatal("日志预检前应已应用配置的时区")
		}
		return
	}
	if mode == "close-timeout" {
		// 故意阻塞关闭回调。若配置未生效，回调会在默认期限前正常返回。
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		plan := bootstrap.Plan{
			Resources: []bootstrap.Resource{
				{
					Name: "slow-close",
					Init: func(_ string, resources *lifecycle.Stack) error {
						return resources.Register(func() error {
							<-ctx.Done()
							return nil
						})
					},
				},
			},
		}
		if err := bootstrap.Run(t.Context(), path, plan); !errors.Is(err, lifecycle.ErrCloseTimeout) {
			t.Fatalf("配置的资源关闭期限未生效: %v", err)
		}
		return
	}
	plan := bootstrap.Plan{
		Prepare: []bootstrap.Step{
			{
				Name: "probe",
				Init: func(_ context.Context, configDir string, resources *lifecycle.Stack) error {
					if configDir != dir || env.ConfigDirPath() != dir || time.Local != env.TimeLocation() || time.Local.String() != "Asia/Shanghai" {
						t.Fatal("配置目录或时区错误")
					}
					fmt.Fprintln(os.Stdout, "stdout probe")
					return resources.Register(func() error {
						logit.Named("audit").Info(context.Background(), "closing resource")
						return nil
					})
				},
			},
		},
	}
	if err := bootstrap.Run(t.Context(), path, plan); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Run(t.Context(), path, bootstrap.Plan{}); !errors.Is(err, env.ErrAlreadyInitialized) {
		t.Fatalf("second run: %v", err)
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
