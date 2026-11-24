// Package elasticsearchboot 在同一资源步骤内装配不同主版本的 Elasticsearch 命名客户端。
// 仅使用 Elasticsearch 8 时，直接将 v8.Instances("default") 加入 bootstrap.Plan.Resources。
// 同时连接多个主版本时，使用 elasticsearchboot.Instances(v7.Named("archive"), v8.Named("default"))。
package elasticsearchboot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/elasticsearchx"
)

// Group 声明使用同一个主版本客户端的实例，由版本子包的 Named 创建。
type Group struct {
	Names    []string
	NewNamed func(elasticsearchx.Config, ...elasticsearchx.Option) (*elasticsearchx.Client, error)
}

// Instances 按组和组内的声明顺序打开实例。各主版本共享名称空间和关闭栈条目。
func Instances(groups ...Group) bootstrap.Resource {
	var names []string
	byName := make(map[string]Group)
	for _, group := range groups {
		names = append(names, group.Names...)
		for _, name := range group.Names {
			byName[name] = group
		}
	}
	open := func(dir, name string, cfg *fileConfig) error {
		return byName[name].open(dir, name, cfg)
	}
	return bootstrap.NamedInstances(constant.ResourceElasticsearch, names, open, elasticsearchx.CloseAll)
}

func (g Group) open(dir, name string, cfg *fileConfig) error {
	if g.NewNamed == nil {
		return errors.New("missing Elasticsearch client constructor")
	}
	clientCfg, err := cfg.clientConfig(dir, name)
	if err != nil {
		return err
	}
	_, err = g.NewNamed(clientCfg,
		elasticsearchx.OptLogRequests(cfg.LogRequests == nil || *cfg.LogRequests),
		elasticsearchx.OptLogDetails(cfg.LogDetails),
	)
	return err
}

type fileConfig struct {
	Addresses      []string      `mapstructure:"addresses"`
	Username       string        `mapstructure:"username"`
	Password       string        `mapstructure:"password"`
	APIKey         string        `mapstructure:"apiKey"`
	CACertFile     string        `mapstructure:"caCertFile"`
	StartupTimeout time.Duration `mapstructure:"startupTimeout"`
	DialTimeout    time.Duration `mapstructure:"dialTimeout"`
	Pool           poolConfig    `mapstructure:"pool"`
	SlowThreshold  time.Duration `mapstructure:"slowThreshold"`
	// 区分省略和显式 false，沿用原配置默认开启请求概要的约定。
	LogRequests *bool `mapstructure:"logRequests"`
	LogDetails  bool  `mapstructure:"logDetails"`
}

type poolConfig struct {
	MaxIdleConns        int           `mapstructure:"maxIdleConns"`
	MaxIdleConnsPerHost int           `mapstructure:"maxIdleConnsPerHost"`
	MaxConnsPerHost     int           `mapstructure:"maxConnsPerHost"`
	IdleConnTimeout     time.Duration `mapstructure:"idleConnTimeout"`
}

func (cfg *fileConfig) clientConfig(dir, name string) (elasticsearchx.Config, error) {
	var ca []byte
	if path := cfg.CACertFile; path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		var err error
		ca, err = os.ReadFile(path)
		if err != nil {
			return elasticsearchx.Config{}, fmt.Errorf("read caCertFile: %w", err)
		}
		// 空字节在底层表示使用系统信任，不能把显式指定的空证书文件当成省略。
		if len(ca) == 0 {
			return elasticsearchx.Config{}, errors.New("empty caCertFile")
		}
	}
	return elasticsearchx.Config{
		Name:                name,
		Addresses:           cfg.Addresses,
		Username:            cfg.Username,
		Password:            cfg.Password,
		APIKey:              cfg.APIKey,
		CACert:              ca,
		StartupTimeout:      cfg.StartupTimeout,
		DialTimeout:         cfg.DialTimeout,
		MaxIdleConns:        cfg.Pool.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.Pool.MaxIdleConnsPerHost,
		MaxConnsPerHost:     cfg.Pool.MaxConnsPerHost,
		IdleConnTimeout:     cfg.Pool.IdleConnTimeout,
		SlowThreshold:       cfg.SlowThreshold,
	}, nil
}
