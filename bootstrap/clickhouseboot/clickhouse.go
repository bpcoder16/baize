// Package clickhouseboot 按实例名装配 ClickHouse 主从连接池。
// 将 clickhouseboot.Instances("default") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 clickhouse.default.yaml，业务通过 clickhousex.Named("default") 借用客户端。
package clickhouseboot

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/clickhousex"
)

// Instances 声明由白泽在启动时创建、退出时统一关闭的 ClickHouse 实例。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceClickHouse, names, open, clickhousex.CloseAll)
}

func open(_, name string, cfg *fileConfig) error {
	clientCfg, err := cfg.clientConfig(name)
	if err != nil {
		return err
	}
	_, err = clickhousex.NewNamed(clientCfg)
	return err
}

type fileConfig struct {
	Master         addressConfig       `mapstructure:"master"`
	Slaves         []addressConfig     `mapstructure:"slaves"`
	Database       string              `mapstructure:"database"`
	Username       string              `mapstructure:"username"`
	Password       string              `mapstructure:"password"`
	Protocol       string              `mapstructure:"protocol"`
	TLS            bool                `mapstructure:"tls"`
	Settings       clickhouse.Settings `mapstructure:"settings"`
	InitTimeout    time.Duration       `mapstructure:"initTimeout"`
	DialTimeout    time.Duration       `mapstructure:"dialTimeout"`
	ReadTimeout    time.Duration       `mapstructure:"readTimeout"`
	Pool           clickhousex.Pool    `mapstructure:"pool"`
	SlowThreshold  time.Duration       `mapstructure:"slowThreshold"`
	LogSQL         *bool               `mapstructure:"logSQL"`
	InterpolateSQL bool                `mapstructure:"interpolateSQL"`
}

type addressConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

func (cfg *fileConfig) clientConfig(name string) (clickhousex.Config, error) {
	var protocol clickhouse.Protocol
	switch cfg.Protocol {
	case "", "native":
		protocol = clickhouse.Native
	case "http":
		protocol = clickhouse.HTTP
	default:
		return clickhousex.Config{}, fmt.Errorf("invalid ClickHouse protocol %q (expected native or http)", cfg.Protocol)
	}
	endpoint := func(addr addressConfig) clickhousex.Endpoint {
		ep := clickhousex.Endpoint{
			Host:        addr.Host,
			Port:        addr.Port,
			Database:    cfg.Database,
			Username:    cfg.Username,
			Password:    cfg.Password,
			Protocol:    protocol,
			Settings:    cfg.Settings,
			DialTimeout: cfg.DialTimeout,
			ReadTimeout: cfg.ReadTimeout,
			Pool:        cfg.Pool,
		}
		if cfg.TLS {
			ep.TLS = &tls.Config{ServerName: addr.Host}
		}
		return ep
	}
	slaves := make([]clickhousex.Endpoint, len(cfg.Slaves))
	for i, addr := range cfg.Slaves {
		slaves[i] = endpoint(addr)
	}
	return clickhousex.Config{
		Name:           name,
		Master:         endpoint(cfg.Master),
		Slaves:         slaves,
		InitTimeout:    cfg.InitTimeout,
		SlowThreshold:  cfg.SlowThreshold,
		LogSQL:         cfg.LogSQL == nil || *cfg.LogSQL,
		InterpolateSQL: cfg.InterpolateSQL,
	}, nil
}
