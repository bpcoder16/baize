package instancekey

import "fmt"

// Lookup Pixiu 的 Named 在实例缺失或已关闭时 panic，仅将查找失败转换为启动错误。
func Lookup[T any](kind, name string, named func(string) T) (client T, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("look up %s %q: %v", kind, name, cause)
		}
	}()
	return named(name), nil
}
