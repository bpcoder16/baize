package share

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// Page 通过稳定名称和请求类型标识分享页面。T 支持结构体及其单层指针。
type Page[T any] string

// Registration 是 Page.Register 返回的启动声明，可与不同请求类型的声明混合装配。
type Registration struct {
	name        string
	requestType reflect.Type
	dispatch    func(*gin.Context, json.RawMessage, binding.StructValidator) error
}

// Register 将页面绑定到类型化处理函数，实际发布在 Prepare 中完成。
func (p Page[T]) Register(handler func(*gin.Context, T)) Registration {
	r := Registration{name: string(p), requestType: reflect.TypeFor[T]()}
	if handler != nil {
		r.dispatch = func(c *gin.Context, raw json.RawMessage, validator binding.StructValidator) error {
			req, err := decodeRequest[T](raw, validator)
			if err != nil {
				return ErrInvalidShare
			}
			handler(c, req)
			return nil
		}
	}
	return r
}

// BuildURL 使用当前活动密钥构造长期分享链接，失败时返回空串。
// ownerID 由业务校验归属后提供，同一页面允许多次分享。
func (p Page[T]) BuildURL(ownerID string, req T) (string, error) {
	s := active.Load()
	if s == nil {
		return "", ErrUnavailable
	}
	r, ok := s.pages[string(p)]
	if !ok || r.requestType != reflect.TypeFor[T]() {
		return "", ErrUnregistered
	}
	if !validOwner(ownerID) {
		return "", ErrInvalidRequest
	}

	raw, err := json.Marshal(&req)
	if err != nil {
		return "", ErrInvalidRequest
	}
	if len(raw) > maxURLBytes {
		return "", ErrURLTooLong
	}
	// 打开端只看得到序列化后的字段，必须以同一解码路径验证最终载荷。
	if _, err = decodeRequest[T](raw, s.validator); err != nil {
		return "", err
	}
	return s.buildURL(string(p), ownerID, raw)
}

func decodeRequest[T any](raw json.RawMessage, validator binding.StructValidator) (T, error) {
	var req T
	// raw 来自 Marshal 或 Unmarshal 到 RawMessage，均从 JSON 值的首字节开始。
	if len(raw) == 0 || raw[0] != '{' {
		return req, ErrInvalidRequest
	}
	// raw 已是单个 JSON 值。动态字段保留数字原文，避免大整数经过 float64 舍入。
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		return req, ErrInvalidRequest
	}
	if err := validator.ValidateStruct(req); err != nil {
		return req, ErrInvalidRequest
	}
	return req, nil
}
