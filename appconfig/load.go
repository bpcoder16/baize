// Package appconfig 读取白泽应用启动配置。Load 可重复调用，环境和日志由 bootstrap 初始化。
package appconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/env"
)

// Config 对应启动文件的三个顶层字段。每次 Load 返回独立的值。
type Config struct {
	Env              env.Config `mapstructure:"env"`
	Log              LogConfig  `mapstructure:"log"`
	DisabledServices []string   `mapstructure:"disabledServices"`
}

var instanceName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Load 按文件后缀严格解码 YAML、TOML 或 JSON，校验禁用规则。
// 配置目录取入口文件的绝对父目录，符号链接使用链接所在目录。
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("appconfig: empty config file path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("appconfig: resolve %q: %w", path, err)
	}
	cfg, err := configx.Parse[Config](abs)
	if err != nil {
		return nil, fmt.Errorf("appconfig: %w", err)
	}
	for i, rule := range cfg.DisabledServices {
		kind, name, named := strings.Cut(rule, ":")
		switch kind {
		case constant.ServiceHTTP, constant.ServiceCron, constant.ServiceNATSConsumer, constant.ServiceWebSocket:
		default:
			return nil, fmt.Errorf("appconfig: %q disabledServices[%d]: unknown service type in %q", abs, i, rule)
		}
		if named && !instanceName.MatchString(name) {
			return nil, fmt.Errorf("appconfig: %q disabledServices[%d]: invalid instance name in %q", abs, i, rule)
		}
	}
	cfg.Env.ConfigDirPath = filepath.Dir(abs)
	return cfg, nil
}
