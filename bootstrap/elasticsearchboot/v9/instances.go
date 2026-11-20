// Package v9 将 Elasticsearch 9 命名客户端接入白泽启动流程。
package v9

import (
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/elasticsearchboot"
	esv9 "github.com/bpcoder16/pixiu/infra/elasticsearchx/v9"
)

// Instances 为 Plan.Resources 声明一个或多个 Elasticsearch 9 实例。
func Instances(names ...string) bootstrap.Resource {
	return elasticsearchboot.Instances(Named(names...))
}

// Named 将这些实例绑定到 Elasticsearch 9，供 elasticsearchboot.Instances 组合多个主版本。
func Named(names ...string) elasticsearchboot.Group {
	return elasticsearchboot.Group{Names: names, NewNamed: esv9.NewNamed}
}
