// Package postgresqlboot 按实例名装配 PostgreSQL 主从连接池。
// 将 postgresqlboot.Instances("default") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 postgresql.default.yaml，业务通过 postgresqlx.Named("default") 借用客户端。
package postgresqlboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/postgresqlx"
)

// Instances 声明由白泽在启动时创建、退出时统一关闭的 PostgreSQL 实例。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourcePostgreSQL, names, open, postgresqlx.CloseAll)
}

func open(name string, cfg *fileConfig) error {
	_, err := postgresqlx.NewNamed(cfg.clientConfig(name))
	return err
}

// 实例身份来自代码声明；主从共用连接参数，默认值及语义校验由 postgresqlx 负责。
type fileConfig struct {
	InitTimeout           time.Duration    `mapstructure:"initTimeout"`
	Master                addressConfig    `mapstructure:"master"`
	Slaves                []addressConfig  `mapstructure:"slaves"`
	Database              string           `mapstructure:"database"`
	Username              string           `mapstructure:"username"`
	Password              string           `mapstructure:"password"`
	Charset               string           `mapstructure:"charset"`
	Location              string           `mapstructure:"location"`
	SSLMode               string           `mapstructure:"sslMode"`
	SSLRootCert           string           `mapstructure:"sslRootCert"`
	SSLCert               string           `mapstructure:"sslCert"`
	SSLKey                string           `mapstructure:"sslKey"`
	DialTimeout           time.Duration    `mapstructure:"dialTimeout"`
	Pool                  postgresqlx.Pool `mapstructure:"pool"`
	SessionTimeZone       string           `mapstructure:"sessionTimeZone"`
	SlowThreshold         time.Duration    `mapstructure:"slowThreshold"`
	DisableStatementCache bool             `mapstructure:"disableStatementCache"`
	// 指针区分省略与显式 false；省略时默认开启，与 MySQL 装配一致。
	LogSQL         *bool `mapstructure:"logSQL"`
	InterpolateSQL *bool `mapstructure:"interpolateSQL"`
}

type addressConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

func (cfg *fileConfig) endpoint(address addressConfig) postgresqlx.Endpoint {
	return postgresqlx.Endpoint{
		Host:        address.Host,
		Port:        address.Port,
		Database:    cfg.Database,
		Username:    cfg.Username,
		Password:    cfg.Password,
		Charset:     cfg.Charset,
		Location:    cfg.Location,
		SSLMode:     cfg.SSLMode,
		SSLRootCert: cfg.SSLRootCert,
		SSLCert:     cfg.SSLCert,
		SSLKey:      cfg.SSLKey,
		DialTimeout: cfg.DialTimeout,
		Pool:        cfg.Pool,
	}
}

func (cfg *fileConfig) clientConfig(name string) postgresqlx.Config {
	slaves := make([]postgresqlx.Endpoint, len(cfg.Slaves))
	for i, address := range cfg.Slaves {
		slaves[i] = cfg.endpoint(address)
	}
	return postgresqlx.Config{
		Name:                  name,
		InitTimeout:           cfg.InitTimeout,
		Master:                cfg.endpoint(cfg.Master),
		Slaves:                slaves,
		SessionTimeZone:       cfg.SessionTimeZone,
		SlowThreshold:         cfg.SlowThreshold,
		LogSQL:                cfg.LogSQL == nil || *cfg.LogSQL,
		InterpolateSQL:        cfg.InterpolateSQL == nil || *cfg.InterpolateSQL,
		DisableStatementCache: cfg.DisableStatementCache,
	}
}
