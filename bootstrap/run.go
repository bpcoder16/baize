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
	resources, err := lifecycle.New(cfg.ResourceCloseTimeout)
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
	if err = runPrepare(ctx, dir, plan.Prepare, resources); err != nil {
		return err
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
