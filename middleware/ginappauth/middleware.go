// Package ginappauth 将 Baize 的应用身份和权限校验绑定到 Gin 路由。
package ginappauth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bpcoder16/baize/httpauth"
	"github.com/bpcoder16/baize/middleware/ginauth"
	"github.com/bpcoder16/baize/service/appauth"
	"github.com/bpcoder16/pixiu/logit"
	"github.com/gin-gonic/gin"
)

// Require 要求已验证的 AppID 具备 permission。writeFailure 可在请求中止后写宿主业务响应。
func Require(permission string, writeFailure ginauth.FailureHandler) gin.HandlerFunc {
	if permission == "" || strings.TrimSpace(permission) != permission {
		panic("ginappauth: invalid route permission")
	}
	permissionField := logit.Str("appauth_permission", permission)
	return func(c *gin.Context) {
		r, err := appauth.Authorize(c.Request, permission)
		if err != nil {
			result, status := failureResult(err)
			defer addAccessFields(c, permissionField, logit.Str("appauth_result", result))
			c.Abort()
			if writeFailure != nil {
				writeFailure(c, err)
				return
			}
			if status == http.StatusUnauthorized {
				c.Header("WWW-Authenticate", `AppSignature realm="api"`)
			}
			c.Status(status)
			return
		}
		c.Request = r
		appID, _ := appauth.AppIDFromContext(r.Context())
		defer addAccessFields(c, permissionField, logit.Str("appauth_result", "allowed"), logit.Str("app_id", appID))
		c.Next()
	}
}

// 在 handler 或失败处理结束后补充请求字段，交由外层 ginx 按原有开关输出 HTTPRequest。
func addAccessFields(c *gin.Context, fields ...logit.Field) {
	ctx := c.Request.Context()
	if !logit.InfoEnabled(ctx) {
		return
	}
	// ginx 已创建请求作用域。独立使用原生 Gin 时也允许外层日志中间件读取这些字段。
	if _, ok := logit.LogIDFromContext(ctx); !ok {
		ctx = logit.WithContext(ctx)
		c.Request = c.Request.WithContext(ctx)
	}
	logit.AddField(ctx, fields...)
}

func failureResult(err error) (string, int) {
	switch {
	case errors.Is(err, httpauth.ErrMissingCredentials):
		return "missing_credentials", http.StatusUnauthorized
	case errors.Is(err, httpauth.ErrInvalidCredentials):
		return "invalid_credentials", http.StatusUnauthorized
	case errors.Is(err, appauth.ErrForbidden):
		return "forbidden", http.StatusForbidden
	case errors.Is(err, appauth.ErrBodyTooLarge):
		return "body_too_large", http.StatusRequestEntityTooLarge
	default:
		return "unavailable", http.StatusServiceUnavailable
	}
}
