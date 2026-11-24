package realtimeboot_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/realtimeboot"
	"github.com/bpcoder16/baize/service/realtime"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/websocketx"
	"github.com/gorilla/websocket"
)

func newServer() (*realtime.Server, error) {
	return realtime.NewServer(realtime.Config{
		Authenticate:     func(context.Context, *http.Request) (realtime.ClientInfo, error) { return realtime.ClientInfo{}, nil },
		RegisterHandlers: func(*realtime.Router) error { return nil },
	})
}

func writeConfig(t *testing.T, dir, filename, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDisabled(t *testing.T) {
	for _, rule := range []string{"realtime", "realtime:app"} {
		dir := t.TempDir()
		writeConfig(t, dir, "app.yaml", "disabledServices: ['"+rule+"']\n")
		plan := bootstrap.Plan{Services: []bootstrap.Service{realtimeboot.Service("app", nil)}}
		err := bootstrap.Run(t.Context(), filepath.Join(dir, "app.yaml"), plan)
		if !errors.Is(err, apprun.ErrNoServices) {
			t.Fatalf("禁用 %s 时得到 %v", rule, err)
		}
	}
}

func TestBuildValidation(t *testing.T) {
	dir := t.TempDir()
	decl := realtimeboot.Service("app", func() (*realtime.Server, error) {
		t.Fatal("无效配置执行了工厂")
		return nil, nil
	})
	if _, err := decl.Build(t.Context(), dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, body := range []string{"path: /ws/app", "addr: ':0'\npath: /ready/ws", "addr: ':0'\npath: ws", "addr: ':0'\npath: /x/../a", "addr: ':0'\npath: /ws\nunknown: true", "addr: ':0'\npath: /ws\nmaxHeaderBytes: -1"} {
		writeConfig(t, dir, "realtime.app.yaml", body)
		if _, err := decl.Build(t.Context(), dir); err == nil {
			t.Fatalf("接受了配置 %q", body)
		}
	}
	writeConfig(t, dir, "realtime.app.yaml", "addr: ':0'\npath: /ws/app")
	for _, service := range []bootstrap.Service{
		realtimeboot.Service("../x", newServer),
		realtimeboot.Service("app", nil),
		realtimeboot.Service("app", func() (*realtime.Server, error) { return nil, nil }),
	} {
		if _, err := service.Build(t.Context(), dir); err == nil {
			t.Fatalf("接受了无效声明 %s", service.Name)
		}
	}
}

func TestListenFailureAndEarlyStop(t *testing.T) {
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	defer listener.Close()
	for _, early := range []bool{false, true} {
		dir := t.TempDir()
		name := uuid.New().String()
		writeConfig(t, dir, "realtime."+name+".yaml", "addr: '"+listener.Addr().String()+"'\npath: /ws/app")
		s, buildErr := realtimeboot.Service(name, newServer).Build(t.Context(), dir)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		t.Cleanup(s.Cancel)
		if realtime.Named(name).Ready() {
			t.Fatal("Build 提前就绪")
		}
		if early {
			if err := s.Stop(); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 1)
		go func() { done <- s.Run() }()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		select {
		case err := <-done:
			if early != (err == nil) {
				t.Fatalf("提前停收 %v, Run 返回 %v", early, err)
			}
		case <-ctx.Done():
			t.Fatal("Run 未退出")
		}
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := s.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
}

func TestDrainInFlightHandler(t *testing.T) {
	entered := make(chan context.Context, 1)
	dir := t.TempDir()
	name := uuid.New().String()
	reserve, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reserve.Addr().String()
	if closeErr := reserve.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	writeConfig(t, dir, "realtime."+name+".yaml", "addr: '"+addr+"'\npath: /ws/app")
	s, err := realtimeboot.Service(name, func() (*realtime.Server, error) {
		return realtime.NewServer(realtime.Config{
			Authenticate: func(context.Context, *http.Request) (realtime.ClientInfo, error) { return realtime.ClientInfo{}, nil },
			RegisterHandlers: func(r *realtime.Router) error {
				return r.Handle("test", "block", func(ctx context.Context, _ *realtime.Session, _ *realtime.Message) error {
					entered <- ctx
					<-ctx.Done()
					return ctx.Err()
				})
			},
		})
	}).Build(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run() }()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	dialer := websocket.Dialer{Subprotocols: []string{realtime.SubprotocolJSON}}
	var c *websocket.Conn
	for {
		c, _, err = dialer.DialContext(ctx, "ws://"+addr+"/ws/app", nil)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer c.Close()
	if writeErr := c.WriteMessage(websocket.TextMessage, []byte(`{"kind":1,"requestId":"r","scene":"test","action":"block","data":{}}`)); writeErr != nil {
		t.Fatal(writeErr)
	}
	var handlerCtx context.Context
	select {
	case handlerCtx = <-entered:
	case <-ctx.Done():
		t.Fatal("Handler 未进入")
	}
	if stopErr := s.Stop(); stopErr != nil {
		t.Fatal(stopErr)
	}
	expired, endWait := context.WithCancel(ctx)
	endWait()
	if waitErr := s.Wait(expired); !errors.Is(waitErr, context.Canceled) || handlerCtx.Err() != nil {
		t.Fatalf("等待在途 Handler 时 Wait=%v, Handler=%v", waitErr, handlerCtx.Err())
	}
	s.Cancel()
	// Handler 报告被取消，排空结果应保留该回调失败。
	if waitErr := s.Wait(ctx); !errors.Is(waitErr, websocketx.ErrDrainFailed) {
		t.Fatal(waitErr)
	}
	select {
	case runErr := <-runDone:
		if !errors.Is(runErr, websocketx.ErrDrainFailed) {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("取消后未排空")
	}
}
