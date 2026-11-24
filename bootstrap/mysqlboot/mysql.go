// Package mysqlboot 按宿主声明的名称，用 Pixiu mysqlx 装配 MySQL 主从连接池。
//
// 将 mysqlboot.Instances("orders", "audit") 加入 bootstrap.Plan.Resources，
// 并在入口配置同目录提供 mysql.orders.yaml 和 mysql.audit.yaml。
// 业务准备和服务可通过 mysqlx.Named("orders").MasterDB(ctx) 访问连接。
// bootstrap.Run 返回前统一关闭连接池。
package mysqlboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/mysqlx"
)

// Instances 声明由白泽管理的一组 MySQL 命名实例。声明时不读文件或建连。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceMySQL, names, open, mysqlx.CloseAll)
}

func open(_, name string, cfg *fileConfig) error {
	_, err := mysqlx.NewNamed(cfg.clientConfig(name))
	return err
}

// 实例名由宿主声明。
// 主从仅配置地址，共用连接参数；零值默认及语义校验由 mysqlx 负责。
type fileConfig struct {
	InitTimeout     time.Duration   `mapstructure:"initTimeout"`
	Master          addressConfig   `mapstructure:"master"`
	Slaves          []addressConfig `mapstructure:"slaves"`
	Database        string          `mapstructure:"database"`
	Username        string          `mapstructure:"username"`
	Password        string          `mapstructure:"password"`
	Charset         string          `mapstructure:"charset"`
	Location        string          `mapstructure:"location"`
	TLSConfig       string          `mapstructure:"tlsConfig"`
	DialTimeout     time.Duration   `mapstructure:"dialTimeout"`
	ReadTimeout     time.Duration   `mapstructure:"readTimeout"`
	WriteTimeout    time.Duration   `mapstructure:"writeTimeout"`
	Pool            mysqlx.Pool     `mapstructure:"pool"`
	SessionTimeZone string          `mapstructure:"sessionTimeZone"`
	SlowThreshold   time.Duration   `mapstructure:"slowThreshold"`
	// 指针区分省略与显式 false；省略时默认开启。
	LogSQL         *bool `mapstructure:"logSQL"`
	InterpolateSQL *bool `mapstructure:"interpolateSQL"`
}

type addressConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

func (cfg *fileConfig) endpoint(address addressConfig) mysqlx.Endpoint {
	return mysqlx.Endpoint{
		Host:         address.Host,
		Port:         address.Port,
		Database:     cfg.Database,
		Username:     cfg.Username,
		Password:     cfg.Password,
		Charset:      cfg.Charset,
		Location:     cfg.Location,
		TLSConfig:    cfg.TLSConfig,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		Pool:         cfg.Pool,
	}
}

func (cfg *fileConfig) clientConfig(name string) mysqlx.Config {
	slaves := make([]mysqlx.Endpoint, len(cfg.Slaves))
	for i, address := range cfg.Slaves {
		slaves[i] = cfg.endpoint(address)
	}
	return mysqlx.Config{
		Name:            name,
		InitTimeout:     cfg.InitTimeout,
		Master:          cfg.endpoint(cfg.Master),
		Slaves:          slaves,
		SessionTimeZone: cfg.SessionTimeZone,
		SlowThreshold:   cfg.SlowThreshold,
		LogSQL:          cfg.LogSQL == nil || *cfg.LogSQL,
		InterpolateSQL:  cfg.InterpolateSQL == nil || *cfg.InterpolateSQL,
	}
}
