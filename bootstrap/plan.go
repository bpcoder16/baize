package bootstrap

import (
	"fmt"
	"strings"
)

// Plan 按顺序初始化共享资源、执行业务准备，再构造全部启用的服务。空服务清单用于一次性任务。
// 每个条目都需要非空的 Name 和回调，Name 在所属阶段内唯一。
type Plan struct {
	Resources []Resource
	Prepare   []Step
	Services  []Service
}

func validateEntry(seen map[string]bool, stage, name string, hasCallback bool) error {
	if strings.TrimSpace(name) == "" || !hasCallback {
		return fmt.Errorf("bootstrap: %s %q requires a name and callback", stage, name)
	}
	if seen[name] {
		return fmt.Errorf("bootstrap: duplicate %s %q", stage, name)
	}
	seen[name] = true
	return nil
}

// validate 在取得资源前校验清单中的声明。
func (p Plan) validate() error {
	if err := validateResources(p.Resources); err != nil {
		return err
	}
	if err := validatePrepare(p.Prepare); err != nil {
		return err
	}
	return validateServices(p.Services)
}
