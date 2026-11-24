package natsboot

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestFileConfig(t *testing.T) {
	cfg, err := configx.Parse[fileConfig](filepath.Join("..", "..", "conf.example", "nats.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.URLs = append(cfg.URLs, "localhost:4223")
	cfg.Username, cfg.Password = "test-user", "test-password"
	cfg.Namespace, cfg.LogDetails = "Test_1", true
	got, err := cfg.clientConfig("events")
	if err != nil {
		t.Fatal(err)
	}
	want := natsx.Config{
		Name:           "events",
		LoggerName:     "nats",
		LogDetails:     true,
		Namespace:      "Test_1",
		URLs:           []string{"nats://127.0.0.1:4222", "localhost:4223"},
		Username:       "test-user",
		Password:       "test-password",
		ConnectTimeout: 5 * time.Second,
		DrainTimeout:   10 * time.Second,
		CloseTimeout:   20 * time.Second,
		JetStream:      natsx.JetStreamConfig{PublishAsyncTimeout: 5 * time.Second},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("命名实例、集群地址、认证、日志或超时配置映射有误")
	}
}

func TestRejectURLCredentials(t *testing.T) {
	for _, address := range []string{"nats://user:private@host", "private@host:4222", "nats://user:private@host:%zz"} {
		cfg := fileConfig{URLs: []string{"nats://localhost:4222", address}}
		_, err := cfg.clientConfig("events")
		if err == nil || !strings.Contains(err.Error(), "urls[1]") || strings.Contains(err.Error(), "private") {
			t.Fatal("应指出错误地址的索引，并省略地址中的凭据")
		}
	}
}

// 只模拟连接握手及排空时的 PING/PONG，测试无需安装 NATS 服务。
func handshakeServer(t *testing.T, reject bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprint(conn, "INFO {\"server_id\":\"test\",\"max_payload\":1048576}\r\n")
		lines := bufio.NewScanner(conn)
		for lines.Scan() {
			if reject && strings.HasPrefix(lines.Text(), "CONNECT ") {
				fmt.Fprint(conn, "-ERR 'Authorization Violation'\r\n")
				return
			}
			if lines.Text() == "PING" {
				fmt.Fprint(conn, "PONG\r\n")
			}
		}
	}()
	return "nats://" + listener.Addr().String()
}

func TestInstances(t *testing.T) {
	// 命名注册表使用进程级关闭状态，每组启动与回收在独立进程验证。
	mode := os.Getenv("BAIZE_NATS_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "second-fails"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInstances$")
				cmd.Env = append(os.Environ(), "BAIZE_NATS_BOOT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := t.TempDir()
	for _, name := range []string{"default", "events"} {
		url := handshakeServer(t, mode == "second-fails" && name == "events")
		if err := os.WriteFile(filepath.Join(dir, "nats."+name+".yaml"), []byte("urls: ["+url+"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var resources lifecycle.Stack
	defer resources.Close()
	err := Instances("default", "events").Init(dir, &resources)
	names := []string{"default"}
	if mode == "second-fails" {
		if !errors.Is(err, natsx.ErrConnect) || !strings.Contains(err.Error(), "nats.events.yaml") {
			t.Fatalf("应报告第二个实例的连接错误及文件路径，实际为 %v", err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, "events")
	}
	var clients []*natsx.Client
	for _, name := range names {
		client := natsx.Named(name)
		if client.Conn().Opts.Name != name || !client.Conn().IsConnected() || client.JetStream() == nil {
			t.Fatalf("NATS 实例 %q 未就绪", name)
		}
		clients = append(clients, client)
	}
	if closeErr := resources.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	for _, client := range clients {
		if !client.Conn().IsClosed() {
			t.Fatal("资源栈未关闭已创建的连接")
		}
	}
}
