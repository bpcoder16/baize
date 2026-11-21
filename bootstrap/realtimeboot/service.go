// Package realtimeboot 装配以 WebSocket 提供服务的命名 Realtime 实例。
package realtimeboot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/internal/instancekey"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/service/realtime"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/httpserver"
)

const readyPath = "/ready/ws"

type fileConfig struct {
	HTTP httpserver.Config `mapstructure:",squash"`
	Path string            `mapstructure:"path"`
}

// Service 声明 realtime:<name>。启用时 Build 读取 realtime.<name>.yaml，再调用工厂。
// 工厂每次返回一个新建、未登记的 Server。业务可通过 realtime.Named(name) 取得它。
// Run 开启独立监听，Stop、Wait 和 Cancel 统一管理 HTTP 请求和已升级的连接。
func Service(name string, newServer func() (*realtime.Server, error)) bootstrap.Service {
	serviceName := constant.ServiceRealtime + ":" + name
	return bootstrap.Service{
		Name: serviceName,
		Build: func(_ context.Context, dir string) (apprun.Service, error) {
			if !instancekey.Valid(name) || newServer == nil {
				return apprun.Service{}, fmt.Errorf("realtimeboot: %q requires a valid instance name and server factory", serviceName)
			}
			filename := filepath.Join(dir, "realtime."+name+".yaml")
			cfg, err := configx.Parse[fileConfig](filename)
			if err != nil {
				return apprun.Service{}, err
			}
			if cfg.HTTP.Addr == "" || strings.TrimSpace(cfg.HTTP.Addr) != cfg.HTTP.Addr {
				return apprun.Service{}, fmt.Errorf("realtimeboot: %s requires addr without surrounding whitespace", filename)
			}
			if !strings.HasPrefix(cfg.Path, "/") || path.Clean(cfg.Path) != cfg.Path ||
				strings.ContainsAny(cfg.Path, "{}?#% \t\r\n") || cfg.Path == readyPath {
				return apprun.Service{}, fmt.Errorf("realtimeboot: %s requires a literal absolute path other than %s", filename, readyPath)
			}
			s := &service{upgradePath: cfg.Path}
			s.http, err = httpserver.New(cfg.HTTP, http.HandlerFunc(s.serveHTTP))
			if err != nil {
				return apprun.Service{}, fmt.Errorf("realtimeboot: create listener from %q: %w", filename, err)
			}
			s.server, err = newServer()
			if err == nil {
				err = realtime.Register(name, s.server)
			}
			if err != nil {
				s.http.Cancel()
				return apprun.Service{}, fmt.Errorf("realtimeboot: build %q: %w", serviceName, err)
			}
			return apprun.Service{
				Name:   serviceName,
				Run:    s.run,
				Stop:   s.stop,
				Wait:   s.wait,
				Cancel: s.cancel,
			}, nil
		},
	}
}

type service struct {
	upgradePath string
	http        *httpserver.Server
	server      *realtime.Server
	started     atomic.Bool
}

func (s *service) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.upgradePath && r.URL.Path != readyPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == readyPath {
		if s.server.Ready() {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		return
	}
	s.server.ServeHTTP(w, r)
}

func (s *service) run() error {
	if !s.started.CompareAndSwap(false, true) {
		return apprun.ErrStarted
	}
	done := make(chan error, 1)
	go func() {
		err := s.server.Run()
		_ = s.http.Stop()
		done <- err
	}()
	runErr := s.http.Run()
	_ = s.server.Stop()
	// 监听失败须立即通知外层管理器，由共享的停机期限约束排空。
	if runErr != nil && !errors.Is(runErr, httpserver.ErrClosed) {
		return runErr
	}
	return <-done
}

func (s *service) stop() error {
	return errors.Join(s.server.Stop(), s.http.Stop())
}

func (s *service) wait(ctx context.Context) error {
	return errors.Join(s.http.Wait(ctx), s.server.Wait(ctx))
}

func (s *service) cancel() {
	s.server.Cancel()
	s.http.Cancel()
}
