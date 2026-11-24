package bootstrap

import "regexp"

var namePartPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidNamePart 判断名称能否用作资源类型、实例名或服务标识的一段。
// 名称至少包含一个字符，仅允许 ASCII 字母、数字、下划线和连字符。
func ValidNamePart(name string) bool { return namePartPattern.MatchString(name) }
