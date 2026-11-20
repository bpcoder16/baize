// Package v7 将 Elasticsearch 7 命名客户端接入白泽启动流程。
package v7

import (
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/elasticsearchboot"
	esv7 "github.com/bpcoder16/pixiu/infra/elasticsearchx/v7"
)

// Instances 为 Plan.Resources 声明一个或多个 Elasticsearch 7 实例。
func Instances(names ...string) bootstrap.Resource {
	return elasticsearchboot.Instances(Named(names...))
}

// Named 将这些实例绑定到 Elasticsearch 7，供 elasticsearchboot.Instances 组合多个主版本。
func Named(names ...string) elasticsearchboot.Group {
	return elasticsearchboot.Group{Names: names, NewNamed: esv7.NewNamed}
}
