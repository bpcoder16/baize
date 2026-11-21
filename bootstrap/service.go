package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bpcoder16/baize/bootstrap/internal/instancekey"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/logit"
)

// Service 是常驻服务的延迟声明。Name 为类型或 类型:实例名，用于配置过滤和运行标识。
// Build 在启用时才调用，成功返回完整的服务回调，失败须自行回收本次部分构造的状态。
// 回调须支持尚未 Run 就停收。Build 使用的 ctx 只用于构造，运行期由 Stop、Wait 和 Cancel 管理。
type Service struct {
	Name  string
	Build func(ctx context.Context, configDir string) (apprun.Service, error)
}

func validateServices(services []Service) error {
	names := make(map[string]bool, len(services))
	for _, service := range services {
		if err := validateEntry(names, "service", service.Name, service.Build != nil); err != nil {
			return err
		}
		kind, name, named := strings.Cut(service.Name, ":")
		if !instancekey.Valid(kind) || (named && !instancekey.Valid(name)) {
			return fmt.Errorf("bootstrap: invalid service name %q", service.Name)
		}
	}
	return nil
}

func (p Plan) enabledServices(disabled []string) []Service {
	var enabled []Service
	for _, service := range p.Services {
		kind, _, _ := strings.Cut(service.Name, ":")
		if !slices.Contains(disabled, kind) && !slices.Contains(disabled, service.Name) {
			enabled = append(enabled, service)
		}
	}
	return enabled
}

func runServices(ctx context.Context, dir string, services []Service, timeout time.Duration) (err error) {
	manager, err := apprun.New(timeout)
	if err != nil {
		return err
	}
	// 构造失败时用已取消的 context 交给同一个管理器。它只停收并排空，绝不会调用 Run。
	pending := false
	defer func() {
		if pending {
			shutdown, cancel := context.WithCancel(context.Background())
			cancel()
			err = errors.Join(err, manager.Run(shutdown))
		}
	}()
	for _, decl := range services {
		if err = ctx.Err(); err != nil {
			return err
		}
		service, buildErr := decl.Build(ctx, dir)
		if buildErr != nil {
			return fmt.Errorf("bootstrap: build service %q: %w", decl.Name, buildErr)
		}
		service.Name = decl.Name
		pending = true
		if registerErr := manager.Register(service); registerErr != nil {
			// Build 已移交所有权。即使回调不完整，仍将可用的关闭回调加入同一排空预算。
			return fmt.Errorf("bootstrap: register service %q: %w", decl.Name,
				errors.Join(registerErr, manager.Register(forRollback(service))))
		}
	}
	logit.Info(ctx, "白泽初始化完成")
	pending = false
	return manager.Run(ctx)
}

// forRollback 只在登记格式错误的服务时使用。管理器已经收到取消通知，永远不会运行该服务。
func forRollback(s apprun.Service) apprun.Service {
	s.Run = func() error { return nil }
	if s.Stop == nil {
		s.Stop = func() error { return nil }
	}
	if s.Wait == nil {
		s.Wait = func(context.Context) error { return apprun.ErrInvalidService }
	}
	if s.Cancel == nil {
		s.Cancel = func() {}
	}
	return s
}
