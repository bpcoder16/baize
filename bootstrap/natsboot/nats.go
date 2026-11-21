// Package natsboot 按宿主声明的名称，用 Pixiu natsx 装配共享的 Core NATS 和 JetStream 客户端。
//
// 将 natsboot.Instances("default", "events") 加入 bootstrap.Plan.Resources，
// 并在入口配置同目录提供 nats.default.yaml 和 nats.events.yaml。
// 业务可用 natsx.Named("events").Publish(ctx, subject, data) 发布 Core 消息。
// 需要订阅时，在 Plan.Services 中声明 ConsumerService("events", jsRegistry, coreRegistry)，
// 实例名与 Instances 对应。消费服务排空后，由资源栈统一关闭客户端。
package natsboot

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/natsx"
)

// Instances 声明由白泽管理的一组 NATS 命名实例。声明时不读文件或建连。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceNATS, names, open, natsx.CloseAll)
}

func open(name string, cfg *fileConfig) error {
	clientCfg, err := cfg.clientConfig(name)
	if err != nil {
		return err
	}
	_, err = natsx.NewNamed(clientCfg)
	return err
}

// 配置文件沿用 Pixiu 的连接参数；实例名由宿主声明。
type fileConfig struct {
	LoggerName     string        `mapstructure:"loggerName"`
	LogDetails     bool          `mapstructure:"logDetails"`
	Namespace      string        `mapstructure:"namespace"`
	URLs           []string      `mapstructure:"urls"`
	Username       string        `mapstructure:"username"`
	Password       string        `mapstructure:"password"`
	ConnectTimeout time.Duration `mapstructure:"connectTimeout"`
	DrainTimeout   time.Duration `mapstructure:"drainTimeout"`
	CloseTimeout   time.Duration `mapstructure:"closeTimeout"`
	JetStream      struct {
		PublishAsyncTimeout time.Duration `mapstructure:"publishAsyncTimeout"`
	} `mapstructure:"jetStream"`
}

func (cfg *fileConfig) clientConfig(name string) (natsx.Config, error) {
	for i, address := range cfg.URLs {
		// 允许省略协议。错误仅标出索引，避免把误填在地址中的凭据写入日志。
		if !strings.Contains(address, "://") {
			address = "nats://" + address
		}
		parsed, err := url.Parse(address)
		if err != nil {
			return natsx.Config{}, fmt.Errorf("invalid urls[%d]", i)
		}
		if parsed.User != nil {
			return natsx.Config{}, fmt.Errorf("urls[%d] contains credentials: use username/password", i)
		}
	}
	return natsx.Config{
		Name:           name,
		LoggerName:     cfg.LoggerName,
		LogDetails:     cfg.LogDetails,
		Namespace:      cfg.Namespace,
		URLs:           cfg.URLs,
		Username:       cfg.Username,
		Password:       cfg.Password,
		ConnectTimeout: cfg.ConnectTimeout,
		DrainTimeout:   cfg.DrainTimeout,
		CloseTimeout:   cfg.CloseTimeout,
		JetStream: natsx.JetStreamConfig{
			PublishAsyncTimeout: cfg.JetStream.PublishAsyncTimeout,
		},
	}, nil
}
