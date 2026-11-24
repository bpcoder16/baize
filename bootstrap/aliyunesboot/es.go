// Package aliyunesboot 按实例名装配阿里云 Elasticsearch 管理端客户端。
// 将 aliyunesboot.Instances("search-control") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 aliyunesx.search-control.yaml，业务通过 esx.Named("search-control") 查询实例或管理词典。
package aliyunesboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/aliyun/esx"
)

// Instances 声明阿里云 ES 管理端命名客户端。每个名称绑定一个云实例，初始化在本地完成。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceAliyunES, names, open, esx.CloseAll)
}

type fileConfig struct {
	Region          string        `mapstructure:"region"`
	InstanceID      string        `mapstructure:"instanceID"`
	AccessKeyID     string        `mapstructure:"accessKeyID"`
	AccessKeySecret string        `mapstructure:"accessKeySecret"`
	Endpoint        string        `mapstructure:"endpoint"`
	RequestTimeout  time.Duration `mapstructure:"requestTimeout"`
	SlowThreshold   time.Duration `mapstructure:"slowThreshold"`
	LogRequests     bool          `mapstructure:"logRequests"`
}

func open(_, name string, cfg *fileConfig) error {
	_, err := esx.NewNamed(esx.Config{
		Name:            name,
		Region:          cfg.Region,
		InstanceID:      cfg.InstanceID,
		AccessKeyID:     cfg.AccessKeyID,
		AccessKeySecret: cfg.AccessKeySecret,
		Endpoint:        cfg.Endpoint,
		RequestTimeout:  cfg.RequestTimeout,
		SlowThreshold:   cfg.SlowThreshold,
	}, esx.OptLogRequests(cfg.LogRequests))
	return err
}
