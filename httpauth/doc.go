// Package httpauth 定义已验证用户身份、标准上下文读写和 HTTP 认证器。
// Bearer 接收签名为 func(context.Context, string) (Principal, error) 的宿主验证函数。
// 默认的认证挑战为 Bearer realm="api"。使用 BearerWithRealm("admin", verify) 可指定其他保护域。
// 凭证无效时可包装 ErrInvalidCredentials，依赖故障应返回原始错误。
// 自定义 Authenticator 可读取 Cookie 或验证请求签名，凭证内容只在验证过程中使用。
//
// 在已通过认证的请求中，业务服务可同时读取路由选择的方案和该方案下的用户 ID：
//
//	p, ok := httpauth.PrincipalFromContext(ctx)
//	if ok {
//		fmt.Println(p.Scheme, p.UserID)
//	}
package httpauth
