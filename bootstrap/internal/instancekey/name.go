// Package instancekey 校验启动配置使用的名称。
package instancekey

import "regexp"

var key = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Valid 判断名称是否可用作实例文件名或服务标识的一部分。
func Valid(name string) bool { return key.MatchString(name) }
