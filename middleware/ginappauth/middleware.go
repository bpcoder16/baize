// Package ginappauth 将 Baize 的应用身份和权限校验绑定到 Gin 路由。
package ginappauth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bpcoder16/baize/httpauth"
	"github.com/bpcoder16/baize/middleware/ginauth"
	"github.com/bpcoder16/baize/service/appauth"
	"github.com/gin-gonic/gin"
)

// Require 要求已验证的 AppID 具备 permission。writeFailure 可在请求中止后写宿主业务响应。
func Require(permission string, writeFailure ginauth.FailureHandler) gin.HandlerFunc {
	if permission == "" || strings.TrimSpace(permission) != permission {
		panic("ginappauth: invalid route permission")
	}
	if writeFailure == nil {
		writeFailure = defaultFailure
	}
	return func(c *gin.Context) {
		r, err := appauth.Authorize(c.Request, permission)
		if err != nil {
			c.Abort()
			writeFailure(c, err)
			return
		}
		c.Request = r
		c.Next()
	}
}

func defaultFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, httpauth.ErrMissingCredentials), errors.Is(err, httpauth.ErrInvalidCredentials):
		c.Header("WWW-Authenticate", `AppSignature realm="api"`)
		c.Status(http.StatusUnauthorized)
	case errors.Is(err, appauth.ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, appauth.ErrBodyTooLarge):
		c.Status(http.StatusRequestEntityTooLarge)
	default:
		c.Status(http.StatusServiceUnavailable)
	}
}
