// Package bootstrap 按宿主提供的入口配置初始化 Pixiu 环境、进程时区和日志。
package bootstrap

import (
	"errors"
	"fmt"
	"time"

	"github.com/bpcoder16/baize/appconfig"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// Init 读取 path 并初始化进程一次。成功后宿主负责关闭返回的资源栈，日志最后关闭。
// 环境与时区在日志预检前生效。后续失败时由 Init 回收已取得的日志资源，宿主应退出进程。
func Init(path string) (*lifecycle.Stack, error) {
	cfg, err := appconfig.Load(path)
	if err != nil {
		return nil, err
	}
	if err = env.Init(cfg.Env); err != nil {
		return nil, fmt.Errorf("bootstrap: initialize environment from %q: %w", path, err)
	}
	time.Local = env.TimeLocation()

	logCfg, err := prepareLogConfig(cfg.Log, cfg.Env.AppName)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %q: %w", path, err)
	}
	var resources lifecycle.Stack
	if err = initLog(logCfg, &resources); err != nil {
		return nil, errors.Join(fmt.Errorf("bootstrap: initialize logs from %q: %w", path, err), resources.Close())
	}
	return &resources, nil
}
