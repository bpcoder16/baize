// Package cronboot 为宿主装配受白泽管理的 Cron 服务。
package cronboot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/infra/env"
)

// Service 声明进程共用的 cron 服务。Build 从入口配置同目录读取 cron.yaml，
// 按 jobs 的名称绑定任务。配置 lock 时借用已初始化的 Redis，首次调度从 Run 开始。
func Service(handlers ...cronx.NamedHandler) bootstrap.Service {
	handlers = slices.Clone(handlers)
	return bootstrap.Service{
		Name: constant.ServiceCron,
		Build: func(_ context.Context, dir string) (apprun.Service, error) {
			return build(dir, handlers)
		},
	}
}

func build(dir string, handlers []cronx.NamedHandler) (apprun.Service, error) {
	path := filepath.Join(dir, "cron.yaml")
	cfg, err := configx.Parse[fileConfig](path)
	if err != nil {
		return apprun.Service{}, err
	}
	jobs, err := cfg.bind(handlers)
	if err != nil {
		return apprun.Service{}, fmt.Errorf("cronboot: bind jobs from %q: %w", path, err)
	}
	options := cronx.Config{
		Location:   env.TimeLocation(),
		LoggerName: cfg.LoggerName,
	}
	if cfg.Lock != nil {
		options.Locker, err = cfg.Lock.locker()
		if err != nil {
			return apprun.Service{}, fmt.Errorf("cronboot: configure lock from %q: %w", path, err)
		}
	}
	scheduler, err := cronx.New(options, jobs...)
	if err != nil {
		return apprun.Service{}, fmt.Errorf("cronboot: create scheduler from %q: %w", path, err)
	}
	return apprun.Service{
		Name: constant.ServiceCron,
		Run: func() error {
			runErr := scheduler.Run()
			// 服务回滚或停机可能先于 Run，已停收的调度器可正常退出。
			if errors.Is(runErr, cronx.ErrClosed) {
				return nil
			}
			return runErr
		},
		Stop:   scheduler.Stop,
		Wait:   scheduler.Wait,
		Cancel: scheduler.Cancel,
	}, nil
}
