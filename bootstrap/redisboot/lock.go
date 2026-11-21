package redisboot

import (
	"context"
	"fmt"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/internal/redisclient"
	"github.com/bpcoder16/pixiu/biz/lockx"
	"github.com/bpcoder16/pixiu/biz/lockx/redislock"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// PrepareLock 借用 redisName 指定的 Redis 实例，创建可通过 lockx.Named(name) 取得的锁执行器。
// redisName 对应 Instances 中的名称，该客户端须配置 maxRetries: -1。
func PrepareLock(name, redisName string, cfg redislock.Config) bootstrap.Step {
	return bootstrap.Step{
		Name: "redis-" + name + "-lock",
		Init: func(context.Context, string, *lifecycle.Stack) error {
			client, err := redisclient.Lookup(redisName)
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
