package bootstrap

import (
	"context"
	"fmt"
	"slices"
)

type instancesKey struct{}

// LookupDeclared 确认实例由本次 Plan 声明，再从注册表借用该实例。
// Pixiu 的 Named 在实例缺失或已关闭时 panic，这里将其转换为启动错误。
func LookupDeclared[T any](ctx context.Context, kind, name string, named func(string) T) (client T, err error) {
	if !slices.Contains(instanceNames(ctx, kind), name) {
		return client, fmt.Errorf("%s instance %q is not declared", kind, name)
	}
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("look up %s %q: %v", kind, name, cause)
		}
	}()
	return named(name), nil
}

func instanceNames(ctx context.Context, kind string) []string {
	resources, _ := ctx.Value(instancesKey{}).([]Resource)
	for _, r := range resources {
		if r.Name == kind {
			return r.instances
		}
	}
	return nil
}
