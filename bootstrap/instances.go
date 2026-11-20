package bootstrap

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

var instanceKey = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// NamedInstances 为同类命名客户端创建一个资源步骤。kind 和实例名只含 ASCII 字母、数字、下划线或连字符。
// 每类声明一次，至少传一个名称。全部 <kind>.<name>.yaml 严格解码成功后，按声明顺序调用 open。
// closeAll 拥有这一类命名客户端的关闭职责，在打开首个实例前登记一次。
func NamedInstances[F any](kind string, names []string, open func(string, *F) error, closeAll func() error) Resource {
	names = slices.Clone(names)
	initGroup := func(configDir string, resources *lifecycle.Stack) error {
		if !instanceKey.MatchString(kind) {
			return fmt.Errorf("invalid instance kind %q", kind)
		}
		if len(names) == 0 {
			return fmt.Errorf("%s requires at least one instance name", kind)
		}
		if open == nil || closeAll == nil {
			return fmt.Errorf("%s requires an opener and closer", kind)
		}
		configPath := func(name string) string {
			return filepath.Join(configDir, kind+"."+name+".yaml")
		}
		seen := make(map[string]bool, len(names))
		configs := make([]*F, len(names))
		for i, name := range names {
			if !instanceKey.MatchString(name) {
				return fmt.Errorf("invalid instance name %q", name)
			}
			if seen[name] {
				return fmt.Errorf("duplicate instance name %q", name)
			}
			seen[name] = true
			path := configPath(name)
			cfg, err := configx.Parse[F](path)
			if err != nil {
				return fmt.Errorf("load %q: %w", path, err)
			}
			configs[i] = cfg
		}
		if err := resources.Register(closeAll); err != nil {
			return fmt.Errorf("register closer: %w", err)
		}
		for i, name := range names {
			if err := open(name, configs[i]); err != nil {
				return fmt.Errorf("open instance %q from %q: %w", name, configPath(name), err)
			}
		}
		return nil
	}
	return Resource{Name: kind, Init: initGroup}
}
