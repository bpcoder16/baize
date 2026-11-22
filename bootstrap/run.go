package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bpcoder16/baize/appconfig"
	"github.com/bpcoder16/pixiu/infra/apprun"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/bpcoder16/pixiu/logit"
)

// Run 读取一次入口配置，执行 plan，并在返回前回收本次创建的资源。
// Run 会设置进程级 time.Local，应在启动读取本地时间或写日志的 goroutine 之前调用。
// 声明服务但全部被禁用时直接返回 apprun.ErrNoServices。常驻期间由 ctx 通知停机。
func Run(ctx context.Context, path string, plan Plan) (err error) {
	if ctx == nil {
		return apprun.ErrNilContext
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = plan.validate(); err != nil {
		return err
	}
	cfg, err := appconfig.Load(path)
	if err != nil {
		return err
	}
	services := plan.enabledServices(cfg.DisabledServices)
	if len(plan.Services) > 0 && len(services) == 0 {
		return apprun.ErrNoServices
	}
	if err = env.Init(cfg.Env); err != nil {
		return fmt.Errorf("bootstrap: initialize environment from %q: %w", path, err)
	}
	time.Local = env.TimeLocation()

	logCfg, err := prepareLogConfig(cfg.Log, cfg.Env.AppName)
	if err != nil {
		return fmt.Errorf("bootstrap: %q: %w", path, err)
	}
	// 在装配层应用默认值，使停机预算与所用 Pixiu 版本解耦。
	closeTimeout := cfg.ResourceCloseTimeout
	if closeTimeout == 0 {
		closeTimeout = 30 * time.Second
	}
	resources, err := lifecycle.New(closeTimeout)
	if err != nil {
		return fmt.Errorf("bootstrap: %q resourceCloseTimeout: %w", path, err)
	}
	defer func() { err = errors.Join(err, resources.Close()) }()
	if err = initLog(logCfg, resources); err != nil {
		return fmt.Errorf("bootstrap: initialize logs from %q: %w", path, err)
	}

	dir := cfg.Env.ConfigDirPath
	if err = initResources(ctx, dir, plan.Resources, resources); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, instancesKey{}, plan.Resources)
	for _, service := range services {
		ctx, err = runPrepareHook(ctx, service.Name, "BeforePrepare", service.BeforePrepare)
		if err != nil {
			return err
		}
	}
	if err = runPrepare(ctx, dir, plan.Prepare, resources); err != nil {
		return err
	}
	for _, service := range services {
		ctx, err = runPrepareHook(ctx, service.Name, "AfterPrepare", service.AfterPrepare)
		if err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(services) == 0 {
		logit.Info(ctx, "白泽初始化完成")
		return nil
	}
	return runServices(ctx, dir, services, cfg.ServiceShutdownTimeout)
}
