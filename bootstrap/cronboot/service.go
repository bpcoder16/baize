// Package cronboot 为宿主装配受白泽管理的 Cron 服务。
package cronboot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/infra/env"
)

// Service 声明进程共用的 cron 服务，并保存注册表当前的任务清单。nil 表示无任务。
// Build 从入口配置同目录读取 cron.yaml，按名称绑定任务，配置 lock 时借用本次 Plan.Resources 声明的 Redis。
func Service(registry *cronx.HandlerRegistry) bootstrap.Service {
	var handlers []cronx.NamedHandler
	if registry != nil {
		handlers = registry.Handlers()
	}
	return bootstrap.Service{
		Name: constant.ServiceCron,
		BeforePrepare: func(ctx context.Context) (context.Context, error) {
			return context.WithValue(ctx, jobsKey{}, &preparedJobs{handlers: handlers}), nil
		},
		Build: func(ctx context.Context, dir string) (apprun.Service, error) {
			var extra []cronx.Job
			if state, ok := ctx.Value(jobsKey{}).(*preparedJobs); ok {
				extra = state.jobs
			}
			return build(ctx, dir, handlers, extra)
		},
	}
}

func build(ctx context.Context, dir string, handlers []cronx.NamedHandler, extra []cronx.Job) (apprun.Service, error) {
	path := filepath.Join(dir, "cron.yaml")
	cfg, err := configx.Parse[fileConfig](path)
	if err != nil {
		return apprun.Service{}, err
	}
	jobs, err := cfg.bind(handlers)
	if err != nil {
		return apprun.Service{}, fmt.Errorf("cronboot: bind jobs from %q: %w", path, err)
	}
	jobs = append(jobs, extra...)
	options := cronx.Config{
		Location:   env.TimeLocation(),
		LoggerName: cfg.LoggerName,
	}
	if cfg.Lock != nil {
		options.Locker, err = cfg.Lock.locker(ctx)
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
