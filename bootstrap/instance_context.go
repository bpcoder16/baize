package bootstrap

import (
	"context"
	"slices"
)

type instancesKey struct{}

// InstanceNames 返回本次 Plan 中由 NamedInstances 声明的指定类型实例。
func InstanceNames(ctx context.Context, kind string) []string {
	resources, _ := ctx.Value(instancesKey{}).([]Resource)
	for _, r := range resources {
		if r.Name == kind {
			return slices.Clone(r.instances)
		}
	}
	return nil
}
