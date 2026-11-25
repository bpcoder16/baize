# Baize 统一分享页面

宿主声明页面名和请求类型，在启动时绑定 Gin handler。签发方传入分享来源的归属 ID 和具体请求，通过 `Build` 取得分享 token，通过 `BuildURL` 取得完整 URL。打开端解密后，在访问者当前的 Gin context 上调用对应 handler。链接默认长期有效，可重复访问。宿主决定页面、路由、鉴权、中间件和响应。

## 注册与调用

页面常量和请求结构体可以放在宿主的共用包。页面名会写入链接，跨版本保持稳定。请求类型支持结构体及其单层指针。

```go
type ProductPageRequest struct {
	ProductID string `json:"productId" binding:"required"`
}

const ProductPage share.Page[ProductPageRequest] = "product.detail.v1"

func ShowProduct(c *gin.Context, req ProductPageRequest) {
	renderProduct(c, req.ProductID) // 宿主按当前访问者和商品状态查询并写响应
}

func prepareShares() bootstrap.Step {
	return shareboot.Prepare(ProductPage.Register(ShowProduct))
}

func productShareURL(ownerID, productID string) (string, error) {
	return ProductPage.BuildURL(ownerID, ProductPageRequest{
		ProductID: productID,
	})
}
```

将 `prepareShares()` 加入 `Plan.Prepare`。同一个 `Prepare` 可接受多种类型的注册声明。注册失败会中止启动。配置、密钥和全部声明验证通过后服务才对业务可见，退出时随启动资源栈释放。

单独签发时调用 `ProductPage.Build(ownerID, req)`。返回值是查询参数 `s` 的完整值，可直接传给 `share.Open(c, token)`。token 最多 2,048 字节。`BuildURL` 的 2,048 字节上限计入页面地址和所有查询参数。

`pageURL` 可以指向宿主直接渲染的页面，也可以指向将 token 转交给业务 API 的前端页面。宿主的打开入口只取一个 `s` 参数。成功时由页面 handler 写响应，用户重复打开或链接预览均会触发读取。需要改变业务状态的操作应由页面引导用户提交至相应的已鉴权接口。

```go
func OpenShare(c *gin.Context) {
	tokens := c.QueryArray("s")
	if len(tokens) != 1 || tokens[0] == "" {
		renderInvalidShare(c)
		return
	}
	if err := share.Open(c, tokens[0]); err != nil {
		renderShareError(c, err)
	}
}
```

## 配置和链接协议

将模板放在 `app.yaml` 同目录。`pageURL` 要求绝对 HTTPS 地址，可以包含路径和固定查询参数，显式端口范围为 1–65535。`s` 为保留参数。用户凭据、片段、越界端口和无效查询都会导致 Prepare 失败。`activeKeyID` 指向 `keys` 中的签发密钥。每项 `id` 必须唯一，`key` 直接填写 32 个安全随机字节的标准 Base64 编码，可用 `openssl rand -base64 32` 生成。真实密钥只放在部署配置中，各环境和宿主独立用钥。[配置模板](../conf.example/baize.share.yaml)

从旧版 `keyEnv` 配置迁移时，把各环境变量原有的 Base64 值原样填入对应 `id` 的 `key` 字段，并移除 `keyEnv`。

形如 `https://m.example.com/share?s=<token>` 的完整 URL 最多 2,048 字节。打开端对 token 先检查长度，再按 `s1.<kid>.<Pixiu token>` 选择密钥，以 `baize.share/s1/<kid>` 为用途调用 Pixiu 的 AES-256-GCM 认证解密。Pixiu token 包含自己的 `v1.` 编码版本和随机 nonce。加密发生在受信任的服务端，同一组密钥的持有者可以生成链接。浏览器只需传递 token。

| 加密字段 | 来源与规则 |
| --- | --- |
| `t` | 签发时的 Unix 秒数。首版只记录时间。 |
| `o` | 宿主已确认的分享来源。1–128 个 UTF-8 字节，保留大小写和前导零，拒绝首尾空白和控制字符。 |
| `p` | 注册的稳定页面名。首位为小写字母，其余可用小写字母、数字、点、下划线和连字符，最多 64 字节。 |
| `r` | 请求 JSON 对象。生成和打开共用同一套还原及校验过程，包括 Gin 的结构体校验器。Go 整数字段按声明的类型还原。 |

使用旧字段 `createdAt`、`ownerId`、`handlerName`、`request` 签发的链接在升级后返回 `ErrInvalidShare`，需重新签发。

资源 ID 和版本号优先使用具体的 `string`、`int64` 或 `uint64` 字段。默认解码到 `any`、`map[string]any` 或 `[]any` 的 JSON 数字使用 `json.Number`，大整数和小数的文本精度都能保留。handler 可按业务类型调用 `Int64()` 并处理越界错误，或用 `String()` 取得十进制原文。升级时检查原先对动态数字使用 `.(float64)` 的 handler，打开历史链接也会按当前版本的规则解码。若签发方已经把整数转成 `float64` 并发生舍入，链接只能保存签发时的值。

业务权限和资源状态在打开时检查。`o` 表示分享来源，访问者身份取自本次请求。以后可在认证解密后、调用 handler 前按归属和场景实施时效或封禁。若不同版本共用封禁规则，应将 `product.detail.v1` 和 `product.detail.v2` 归入同一业务场景。宿主应从网关和应用日志中排除完整链接、token 与敏感请求内容，分享页面可设置 `Referrer-Policy: no-referrer`。

## 密钥轮换与旧链接

1. 保留 `k1` 的原始密钥，为 `k2` 生成一把新密钥。将下例中的占位符替换为实际密钥，把配置部署到所有副本。每次 Prepare 读取一次配置。

   ```yaml
   pageURL: "https://m.example.com/share"
   activeKeyID: k1
   keys:
     - id: k1
       key: "<k1 原有密钥的 Base64 值>"
     - id: k2
       key: "<k2 新生成密钥的 Base64 值>"
   ```

2. 在部署中用轮换前保存的一条 k1 链接验证旧密钥和页面，再切换到 `activeKeyID: k2`。用 k2 新链接和 k1 旧链接分别检查所有打开端，回滚目标也需能打开两者。若配置中 k1 的值被误覆盖为另一把合法密钥，k1 历史链接会认证失败。
3. 需要继续服务旧链接时保留 k1。移除 k1 会使这把密钥签发的所有链接失效。随机 nonce 的 GCM 要求同一密钥跨副本累计签发次数不超过 2³² 次，需提前轮换。[Go cipher 文档](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce)

页面迁址后，旧入口仍需服务，或将原 `s` 参数完整带到新入口。

## request 演进与回滚

同一个页面名下，宿主应使用两版真实的 request 结构验证以下两个方向，并断言资源与展示结果。

| 发布方向 | 应验证的行为 |
| --- | --- |
| 旧版签发 → 新版打开 | 新 handler 接受缺少新增可选字段的旧数据，其零值或业务默认值保留旧页面的含义。 |
| 新版签发 → 旧版打开 | 旧 handler 可以忽略新字段，忽略后仍展示同一资源和版本。例如仅用于来源统计的 `source` 可按此方式添加。 |

若新字段决定商品版本，例如 `edition=deluxe`，旧 handler 忽略它就可能展示普通版。此时以新的页面名（如 `product.detail.v2`）签发，并保留 v1 的请求结构、校验和适配函数。字段改名、类型变化或收紧必填规则也应按此方式处理。先将两个 handler 部署到全部打开端及可回滚版本，再签发 v2。只认识 v1 的副本收到 v2 会返回 `ErrInvalidShare`。两版 handler 可以复用同一个业务 service。

## 错误与兼容测试

| 错误 | 调用方处理 |
| --- | --- |
| `share.ErrUnavailable` | 服务尚未装配或已经释放，按服务不可用处理。 |
| `share.ErrUnregistered` | 签发使用的页面名或请求类型未在当前启动中注册。 |
| `share.ErrInvalidRequest` | 检查归属 ID 和序列化后的请求。 |
| `share.ErrURLTooLong` | token 或完整 URL 超长。可缩小请求，构建 URL 时也可缩短页面地址或固定查询参数。 |
| `share.ErrInvalidShare` | token、密钥编号、载荷或目标页面无效。Open 在调用 handler 前返回，宿主可展示统一错误页。 |

服务测试用独立生成的短字段 URL 验证密钥切换与误覆盖，并覆盖两版 request 双向打开、动态数字精度以及缺少版本化 handler 的回滚。装配测试覆盖配置文件中的密钥缺失、格式错误、编号重复及端口越界时的启动失败行为。[服务测试](../service/share/share_test.go) · [装配测试](../bootstrap/shareboot/prepare_test.go)
