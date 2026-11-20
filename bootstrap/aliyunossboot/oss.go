// Package aliyunossboot 按实例名装配阿里云 OSS 客户端，每个实例绑定一个 Bucket。
// 将 aliyunossboot.Instances("default") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 aliyunossx.default.yaml，业务通过 ossx.Named("default") 访问。
package aliyunossboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/aliyun/ossx"
)

// Instances 声明由白泽管理的 OSS 命名实例。初始化在本地完成。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceAliyunOSS, names, open, ossx.CloseAll)
}

type fileConfig struct {
	Region          string        `mapstructure:"region"`
	Bucket          string        `mapstructure:"bucket"`
	AccessKeyID     string        `mapstructure:"accessKeyID"`
	AccessKeySecret string        `mapstructure:"accessKeySecret"`
	Endpoint        string        `mapstructure:"endpoint"`
	RequestTimeout  time.Duration `mapstructure:"requestTimeout"`
	SlowThreshold   time.Duration `mapstructure:"slowThreshold"`
	LogRequests     bool          `mapstructure:"logRequests"`
}

func open(name string, cfg *fileConfig) error {
	_, err := ossx.NewNamed(ossx.Config{
		Name:            name,
		Region:          cfg.Region,
		Bucket:          cfg.Bucket,
		AccessKeyID:     cfg.AccessKeyID,
		AccessKeySecret: cfg.AccessKeySecret,
		Endpoint:        cfg.Endpoint,
		RequestTimeout:  cfg.RequestTimeout,
		SlowThreshold:   cfg.SlowThreshold,
	}, ossx.OptLogRequests(cfg.LogRequests))
	return err
}
