package ginauth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/bpcoder16/baize/httpauth"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"
)

// FailureHandler 在请求中止后同步处理原始错误，须支持并发调用。
type FailureHandler func(*gin.Context, error)

// Guard 将一个认证器绑定到指定业务方案，供同组路由复用。
type Guard struct {
	scheme       string
	auth         httpauth.Authenticator
	writeFailure FailureHandler
}

// New 在路由构造阶段绑定认证器，配置错误时 panic。
// writeFailure 为 nil 时使用默认响应，此时 auth 必须提供适用的认证挑战头。
func New(scheme string, auth httpauth.Authenticator, writeFailure FailureHandler) *Guard {
	if scheme == "" || strings.TrimSpace(scheme) != scheme {
		panic("ginauth: invalid scheme name")
	}
	if auth.Authenticate == nil {
		panic("ginauth: nil authenticator")
	}
	if writeFailure == nil {
		if strings.TrimSpace(auth.Challenge) == "" || !httpguts.ValidHeaderFieldValue(auth.Challenge) {
			panic("ginauth: default failure handler requires a valid WWW-Authenticate challenge")
		}
		writeFailure = func(c *gin.Context, err error) {
			if errors.Is(err, httpauth.ErrMissingCredentials) || errors.Is(err, httpauth.ErrInvalidCredentials) {
				c.Header("WWW-Authenticate", auth.Challenge)
				c.Status(http.StatusUnauthorized)
				return
			}
			c.Status(http.StatusInternalServerError)
		}
	}
	return &Guard{
		scheme:       scheme,
		auth:         auth,
		writeFailure: writeFailure,
	}
}

// Require 仅允许身份已验证的请求进入后续处理函数。
func (g *Guard) Require() gin.HandlerFunc {
	return g.handler(false)
}

// Optional 在凭证缺失时允许游客访问。提供凭证后的所有验证错误均会中止请求。
func (g *Guard) Optional() gin.HandlerFunc {
	return g.handler(true)
}

func (g *Guard) handler(optional bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, err := g.authenticate(c.Request, optional)
		if err != nil {
			c.Abort()
			g.writeFailure(c, err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func (g *Guard) authenticate(r *http.Request, optional bool) (context.Context, error) {
	p, present, err := g.auth.Authenticate(r)
	if err != nil {
		return nil, err
	}
	if present {
		p.Scheme = g.scheme
		return httpauth.WithPrincipal(r.Context(), p)
	}
	if p != (httpauth.Principal{}) {
		return nil, errors.New("ginauth: authenticator returned an identity without credentials")
	}
	if optional {
		return httpauth.WithoutPrincipal(r.Context()), nil
	}
	return nil, httpauth.ErrMissingCredentials
}
