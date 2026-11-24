package redisboot

import (
	"context"
	"fmt"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/service/lockx"
	"github.com/bpcoder16/baize/service/lockx/redislock"
	"github.com/bpcoder16/pixiu/infra/redisx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// PrepareLock 借用 redisName 指定的 Redis 实例，创建可通过 lockx.Named(name) 取得的锁执行器。
// redisName 须在本次 Plan.Resources 中通过 Instances 声明，该客户端须配置 maxRetries: -1。
func PrepareLock(name, redisName string, cfg redislock.Config) bootstrap.Step {
	return bootstrap.Step{
		Name: "baize.redis-" + name + "-lock",
		Init: func(ctx context.Context, _ string, _ *lifecycle.Stack) error {
			client, err := bootstrap.LookupDeclared(ctx, constant.ResourceRedis, redisName, redisx.Named)
			if err != nil {
				return err
			}
			backend, err := redislock.New(client, cfg)
			if err != nil {
				return fmt.Errorf("Redis %q: %w", redisName, err)
			}
			_, err = lockx.NewNamed(name, backend)
			return err
		},
	}
}
