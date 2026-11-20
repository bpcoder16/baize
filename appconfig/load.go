package appconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/env"
)

// Config 对应应用启动文件。每次 Load 返回独立的值。
type Config struct {
	Env              env.Config `mapstructure:"env"`
	Log              LogConfig  `mapstructure:"log"`
	DisabledServices []string   `mapstructure:"disabledServices"`

	// ResourceCloseTimeout 是整个资源栈共享的关闭期限，零值使用 lifecycle 默认的 20 秒。
	ResourceCloseTimeout time.Duration `mapstructure:"resourceCloseTimeout"`
}

var instanceName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Load 按文件后缀严格解码 YAML、TOML 或 JSON，校验资源关闭期限和服务禁用规则。
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
	if cfg.ResourceCloseTimeout < 0 {
		return nil, fmt.Errorf("appconfig: %q resourceCloseTimeout must be non-negative, got %s", abs, cfg.ResourceCloseTimeout)
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
