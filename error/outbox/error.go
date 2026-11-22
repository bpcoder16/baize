// Package outbox 定义 Outbox 各层共用的错误和错误文本格式。
package outbox

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalidArgument = errors.New("outbox: invalid argument")
	ErrInvalidResource = errors.New("outbox: invalid target resource")
)

// ErrorText 生成适合记录在 last_error 和日志中的有界 UTF-8 文本。
func ErrorText(err error) (text string) {
	defer func() {
		if recover() != nil {
			text = "outbox: error formatting panicked"
		}
	}()
	text = strings.ToValidUTF8(err.Error(), "�")
	// PostgreSQL text 拒绝零字节，替换后再按最终字节数截断。
	text = strings.ReplaceAll(text, "\x00", "�")
	if len(text) > 4096 {
		text = text[:4096]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}
