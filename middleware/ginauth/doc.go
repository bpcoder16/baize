// Package ginauth 将 HTTP 认证器绑定到 Gin 路由。
// 宿主在路由注册回调中构造 Guard，按路由选择必需认证或允许游客访问。
// 假设 verifyApp 已实现令牌验证，me 和 feed 是宿主的 gin.HandlerFunc：
//
//	app := ginauth.New("app", httpauth.Bearer(verifyApp), nil)
//	router.GET("/me", app.Require(), me)
//	router.GET("/feed", app.Optional(), feed)
//
// 默认认证失败响应使用 HTTP 401 和所绑定认证器的 WWW-Authenticate 挑战头。
// 依赖故障、验证器意外返回空白用户 ID 等错误使用 HTTP 500，两种响应均为空正文。
// 宿主可在 New 的第三个参数传入失败处理函数，在请求已中止后写业务响应并记录错误。
// 匿名访问时 PrincipalFromContext 返回 false；来源端等路由信息由宿主独立传递。
package ginauth
