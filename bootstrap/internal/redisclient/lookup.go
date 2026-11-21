package redisclient

import (
	"fmt"

	"github.com/bpcoder16/pixiu/infra/redisx"
)

// Lookup 借用命名客户端，将实例缺失或已关闭导致的 panic 转为装配错误。
func Lookup(name string) (client *redisx.Client, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("look up Redis %q: %v", name, cause)
		}
	}()
	return redisx.Named(name), nil
}
