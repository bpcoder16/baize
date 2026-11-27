// Package appauthboot 在启动时装配服务端应用鉴权。
package appauthboot

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/service/appauth"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/infra/redisx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// Prepare 读取 baize.appauth.yaml，借用本次 Plan 声明的 Redis 保存防重放记录。
func Prepare(redisName string) bootstrap.Step {
	return bootstrap.Step{
		Name: "baize.appauth",
		Init: func(ctx context.Context, dir string, stack *lifecycle.Stack) error {
			instance, err := bootstrap.LookupDeclared(ctx, constant.ResourceRedis, redisName, redisx.Named)
			if err != nil {
				return err
			}
			release, err := appauth.Load(filepath.Join(dir, "baize.appauth.yaml"), env.AppName(), instance.Client())
			if err != nil {
				return err
			}
			if err = stack.Register(release); err != nil {
				return errors.Join(err, release())
			}
			return nil
		},
	}
}
