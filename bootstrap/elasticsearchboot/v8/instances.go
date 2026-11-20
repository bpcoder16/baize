// Package v8 将 Elasticsearch 8 命名客户端接入白泽启动流程。
package v8

import (
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/elasticsearchboot"
	esv8 "github.com/bpcoder16/pixiu/infra/elasticsearchx/v8"
)

// Instances 为 Plan.Resources 声明一个或多个 Elasticsearch 8 实例。
func Instances(names ...string) bootstrap.Resource {
	return elasticsearchboot.Instances(Named(names...))
}

// Named 将这些实例绑定到 Elasticsearch 8，供 elasticsearchboot.Instances 组合多个主版本。
func Named(names ...string) elasticsearchboot.Group {
	return elasticsearchboot.Group{Names: names, NewNamed: esv8.NewNamed}
}
