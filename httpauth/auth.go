package httpauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var (
	// ErrMissingCredentials 表示请求未提供所选方案的凭证。
	ErrMissingCredentials = errors.New("httpauth: missing credentials")
	// ErrInvalidCredentials 表示凭证格式错误或验证未通过。
	ErrInvalidCredentials = errors.New("httpauth: invalid credentials")
)

// Principal 是请求中已验证的身份。认证器填写 UserID，路由绑定时选定的名称写入 Scheme。
type Principal struct {
	UserID string
	Scheme string
}

func (p Principal) validateUserID() error {
	if strings.TrimSpace(p.UserID) == "" {
		return errors.New("httpauth: authenticated principal requires a user ID")
	}
	return nil
}

// Authenticator 描述一种 HTTP 凭证及其验证方式，Authenticate 须支持并发调用。
type Authenticator struct {
	// Challenge 是默认 401 响应的 WWW-Authenticate 值。Bearer 自动生成，自定义认证器按协议填写。
	// 使用自定义失败处理函数时可省略。
	Challenge string
	// Authenticate 仅在确实缺少凭证时返回零身份、false、nil。
	// 已提供凭证时返回验证后的身份、true、nil，或返回错误。任何错误都会中止请求。
	Authenticate func(r *http.Request) (principal Principal, present bool, err error)
}

type principalKey struct{}

// WithPrincipal 将受信任调用方已验证的身份按值写入上下文。空白 ID 或方案返回错误。
func WithPrincipal(ctx context.Context, p Principal) (context.Context, error) {
	if err := p.validateUserID(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Scheme) == "" {
		return nil, errors.New("httpauth: authenticated principal requires a scheme")
	}
	return context.WithValue(ctx, principalKey{}, p), nil
}

// PrincipalFromContext 返回身份副本。游客返回零值和 false。
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// WithoutPrincipal 派生游客上下文，屏蔽外层身份，保留请求的取消、期限及其他值。
func WithoutPrincipal(ctx context.Context) context.Context {
	if _, ok := PrincipalFromContext(ctx); ok {
		return context.WithValue(ctx, principalKey{}, nil)
	}
	return ctx
}
