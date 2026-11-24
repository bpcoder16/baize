// Package aliyunstsboot 按实例名装配阿里云 STS 客户端。
// 将 aliyunstsboot.Instances("oss") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 aliyunstsx.oss.yaml，业务通过 stsx.Named("oss").AssumeRole 申请临时凭据。
package aliyunstsboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/aliyun/stsx"
)

// Instances 声明由白泽管理的 STS 命名实例。初始化在本地完成。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceAliyunSTS, names, open, stsx.CloseAll)
}

type fileConfig struct {
	Region          string        `mapstructure:"region"`
	AccessKeyID     string        `mapstructure:"accessKeyID"`
	AccessKeySecret string        `mapstructure:"accessKeySecret"`
	Endpoint        string        `mapstructure:"endpoint"`
	RequestTimeout  time.Duration `mapstructure:"requestTimeout"`
	SlowThreshold   time.Duration `mapstructure:"slowThreshold"`
	LogRequests     bool          `mapstructure:"logRequests"`
}

func open(_, name string, cfg *fileConfig) error {
	_, err := stsx.NewNamed(stsx.Config{
		Name:            name,
		Region:          cfg.Region,
		AccessKeyID:     cfg.AccessKeyID,
		AccessKeySecret: cfg.AccessKeySecret,
		Endpoint:        cfg.Endpoint,
		RequestTimeout:  cfg.RequestTimeout,
		SlowThreshold:   cfg.SlowThreshold,
	}, stsx.OptLogRequests(cfg.LogRequests))
	return err
}
