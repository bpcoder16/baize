package cronboot

import (
	"errors"
	"strings"
	"time"

	"github.com/bpcoder16/baize/bootstrap/internal/redisclient"
	redislock "github.com/go-co-op/gocron-redis-lock/v2"
	"github.com/go-co-op/gocron/v2"
)

type lockConfig struct {
	RedisName     string        `mapstructure:"redisName"`
	KeyPrefix     string        `mapstructure:"keyPrefix"`
	TTL           time.Duration `mapstructure:"ttl"`
	RenewInterval time.Duration `mapstructure:"renewInterval"`
}

func (cfg lockConfig) locker() (gocron.Locker, error) {
	if cfg.RedisName == "" || strings.TrimSpace(cfg.RedisName) != cfg.RedisName {
		return nil, errors.New("invalid redisName")
	}
	if cfg.KeyPrefix == "" || strings.TrimSpace(cfg.KeyPrefix) != cfg.KeyPrefix {
		return nil, errors.New("invalid keyPrefix")
	}
	if cfg.TTL < 0 {
		return nil, errors.New("ttl must be nonnegative")
	}
	if cfg.TTL == 0 {
		cfg.TTL = 30 * time.Second
	}
	if cfg.RenewInterval == 0 {
		cfg.RenewInterval = cfg.TTL / 3
	}
	if cfg.RenewInterval <= 0 || cfg.RenewInterval >= cfg.TTL {
		return nil, errors.New("renewInterval must be positive and less than ttl")
	}
	client, err := redisclient.Lookup(cfg.RedisName)
	if err != nil {
		return nil, err
	}
	// 每轮只竞争一次，持锁期间自动续租；连接池由 Redis 资源回收。
	return redislock.NewRedisLockerWithOptions(
		client.Client(),
		redislock.WithKeyPrefix(cfg.KeyPrefix),
		redislock.WithAutoExtendDuration(cfg.RenewInterval),
		redislock.WithRedsyncOptions(redislock.WithExpiry(cfg.TTL), redislock.WithTries(1)),
	)
}
