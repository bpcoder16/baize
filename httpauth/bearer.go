package httpauth

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// RFC 6750 §2.1 的 b64token 只允许在末尾填充等号。
var bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

// BearerToken 从唯一的 Authorization 头提取 Bearer 凭证。只有头缺失才返回 ErrMissingCredentials。
func BearerToken(r *http.Request) (string, error) {
	headers := r.Header.Values("Authorization")
	if len(headers) == 0 {
		return "", ErrMissingCredentials
	}
	if len(headers) != 1 {
		return "", ErrInvalidCredentials
	}
	// 字段值两端允许 HTTP 可选空白，但方案和凭证之间只允许一个或多个 ASCII 空格。
	scheme, rest, ok := strings.Cut(strings.Trim(headers[0], " \t"), " ")
	token := strings.TrimLeft(rest, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !bearerTokenPattern.MatchString(token) {
		return "", ErrInvalidCredentials
	}
	return token, nil
}

// Bearer 使用默认保护域 "api" 创建 HTTP Bearer 认证器。无效令牌应返回 ErrInvalidCredentials，
// 运行错误按原样交给失败处理函数。verify 为 nil 时在装配阶段 panic。
func Bearer(verify func(context.Context, string) (Principal, error)) Authenticator {
	return BearerWithRealm("api", verify)
}

// BearerWithRealm 使用指定保护域创建 Bearer 认证器。realm 为原始值，双引号和反斜杠自动转义。
// realm 含 HTTP 头字段禁止的控制字符或 verify 为 nil 时，在装配阶段 panic。
func BearerWithRealm(realm string, verify func(context.Context, string) (Principal, error)) Authenticator {
	if verify == nil {
		panic("httpauth: nil bearer verifier")
	}
	return Authenticator{
		Challenge: bearerChallenge(realm),
		Authenticate: func(r *http.Request) (Principal, bool, error) {
			token, err := BearerToken(r)
			if errors.Is(err, ErrMissingCredentials) {
				return Principal{}, false, nil
			}
			if err != nil {
				return Principal{}, true, err
			}
			p, err := verify(r.Context(), token)
			if err == nil {
				err = p.validateUserID()
			}
			if err != nil {
				return Principal{}, true, err
			}
			return p, true, nil
		},
	}
}

func bearerChallenge(realm string) string {
	var challenge strings.Builder
	challenge.WriteString(`Bearer realm="`)
	// 按 HTTP quoted-string 逐字节编码，保留保护域原值（RFC 9110 §5.6.4）。
	for i := 0; i < len(realm); i++ {
		b := realm[i]
		switch {
		case b == '"' || b == '\\':
			challenge.WriteByte('\\')
		case (b < ' ' && b != '\t') || b == 0x7f:
			panic("httpauth: invalid control character in bearer realm")
		}
		challenge.WriteByte(b)
	}
	challenge.WriteByte('"')
	return challenge.String()
}
