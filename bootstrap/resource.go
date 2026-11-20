package bootstrap

import (
	"context"
	"fmt"

	"github.com/bpcoder16/pixiu/lifecycle"
)

// Resource 是日志就绪后执行的资源初始化步骤。Init 接收入口配置所在目录，
// 每取得一项资源就把关闭函数登记到同一栈，供退出或后续初始化失败时逆序回收。
type Resource struct {
	Name string
	Init func(configDir string, resources *lifecycle.Stack) error
}

func validateResources(resources []Resource) error {
	names := make(map[string]bool, len(resources))
	for _, resource := range resources {
		if err := validateEntry(names, "resource", resource.Name, resource.Init != nil); err != nil {
			return err
		}
	}
	return nil
}

func initResources(ctx context.Context, dir string, steps []Resource, resources *lifecycle.Stack) error {
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step.Init(dir, resources); err != nil {
			return fmt.Errorf("bootstrap: initialize resource %q: %w", step.Name, err)
		}
	}
	return nil
}
