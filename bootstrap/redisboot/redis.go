// Package redisboot 按宿主声明的名称，用 Pixiu redisx 装配单节点 TCP Redis 客户端。
//
// 将 redisboot.Instances("default", "session") 加入 bootstrap.Plan.Resources，
// 并在入口配置同目录提供 redis.default.yaml 和 redis.session.yaml。
// 业务准备和服务可通过 redisx.Named("session").Client() 执行命令。
// bootstrap.Run 返回前统一关闭客户端。
package redisboot

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/redisx"
	"github.com/redis/go-redis/v9"
)

// Instances 声明由白泽管理的一组 Redis 命名实例。声明时不读文件或建连。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceRedis, names, open, redisx.CloseAll)
}

func open(_, name string, cfg *fileConfig) error {
	client, err := cfg.clientConfig(name)
	if err != nil {
		return err
	}
	_, err = redisx.NewNamed(client)
	return err
}

// 保留 Pixiu 和 Biucards 的单节点 YAML 配置键；实例名由宿主注入。
type fileConfig struct {
	Host          string        `mapstructure:"host"`
	Port          int           `mapstructure:"port"`
	Username      string        `mapstructure:"username"`
	Password      string        `mapstructure:"password"`
	DB            int           `mapstructure:"db"`
	DialTimeout   time.Duration `mapstructure:"dialTimeout"`
	ReadTimeout   time.Duration `mapstructure:"readTimeout"`
	WriteTimeout  time.Duration `mapstructure:"writeTimeout"`
	MaxRetries    int           `mapstructure:"maxRetries"`
	Pool          poolConfig    `mapstructure:"pool"`
	SlowThreshold time.Duration `mapstructure:"slowThreshold"`
	LogCommands   bool          `mapstructure:"logCommands"`
}

type poolConfig struct {
	Size            int           `mapstructure:"size"`
	MaxActiveConns  int           `mapstructure:"maxActiveConns"`
	MinIdleConns    int           `mapstructure:"minIdleConns"`
	MaxIdleConns    int           `mapstructure:"maxIdleConns"`
	Timeout         time.Duration `mapstructure:"timeout"`
	ConnMaxIdleTime time.Duration `mapstructure:"connMaxIdleTime"`
	ConnMaxLifetime time.Duration `mapstructure:"connMaxLifetime"`
}

func (cfg *fileConfig) clientConfig(name string) (redisx.Config, error) {
	if cfg.Host == "" || strings.TrimSpace(cfg.Host) != cfg.Host {
		return redisx.Config{}, errors.New("host is required and must not have surrounding whitespace")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return redisx.Config{}, errors.New("invalid port")
	}
	if cfg.DB < 0 {
		return redisx.Config{}, errors.New("negative db")
	}
	if cfg.MaxRetries < -1 {
		return redisx.Config{}, errors.New("maxRetries must be -1 or nonnegative")
	}
	// 部署配置只接受非负超时；连接池大小和慢命令阈值由 redisx 校验。
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{"dialTimeout", cfg.DialTimeout},
		{"readTimeout", cfg.ReadTimeout},
		{"writeTimeout", cfg.WriteTimeout},
		{"pool.timeout", cfg.Pool.Timeout},
		{"pool.connMaxIdleTime", cfg.Pool.ConnMaxIdleTime},
		{"pool.connMaxLifetime", cfg.Pool.ConnMaxLifetime},
	} {
		if setting.value < 0 {
			return redisx.Config{}, fmt.Errorf("negative %s", setting.name)
		}
	}
	port := cfg.Port
	if port == 0 {
		port = 6379
	}
	return redisx.Config{
		Name: name,
		Options: redis.Options{
			Addr:            net.JoinHostPort(cfg.Host, strconv.Itoa(port)),
			Username:        cfg.Username,
			Password:        cfg.Password,
			DB:              cfg.DB,
			DialTimeout:     cfg.DialTimeout,
			ReadTimeout:     cfg.ReadTimeout,
			WriteTimeout:    cfg.WriteTimeout,
			MaxRetries:      cfg.MaxRetries,
			PoolSize:        cfg.Pool.Size,
			MaxActiveConns:  cfg.Pool.MaxActiveConns,
			MinIdleConns:    cfg.Pool.MinIdleConns,
			MaxIdleConns:    cfg.Pool.MaxIdleConns,
			PoolTimeout:     cfg.Pool.Timeout,
			ConnMaxIdleTime: cfg.Pool.ConnMaxIdleTime,
			ConnMaxLifetime: cfg.Pool.ConnMaxLifetime,
		},
		SlowThreshold: cfg.SlowThreshold,
		LogCommands:   cfg.LogCommands,
	}, nil
}
