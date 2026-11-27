# AppID 请求签名

Go 调用方导入 `github.com/bpcoder16/baize/service/appauth/appsign` 即可创建带签名的 Pixiu `httpcall` 客户端，其他语言可按下述字节约定生成相同的四个请求头。调用方持有目标服务分配的 AppID 和 AppSecret。HMAC-SHA256 校验请求完整性与调用方身份，生产环境通过 HTTPS 传输。

## 签名协议 v1

AppID 匹配 `[A-Za-z0-9._-]{1,128}`。AppSecret 由 32 个随机字节编码为 64 位十六进制字符串。计算 HMAC 时先解码，使用这 32 个原始字节作为密钥。各目标服务、各环境分别发放密钥。

协议适用于使用路径和查询串作为请求目标的 HTTP API。CONNECT 隧道使用主机和端口作为请求目标，超出此协议范围。

签名原文按下表顺序拼接，字段之间插入一个 LF 字节（`0x0a`），末尾没有换行，然后使用 UTF-8 编码。

| 顺序 | 字段 | 取值 |
| --- | --- | --- |
| 1 | AppID | `X-App-Id` 的值 |
| 2 | 时间戳 | `X-Timestamp` 的值，Unix 秒数的十进制字符串 |
| 3 | nonce | `X-Nonce` 的值，16 个随机字节编码成 32 位小写十六进制 |
| 4 | HTTP 方法 | 请求实际使用的方法，例如 `POST` |
| 5 | 请求目标 | 实际发送的已转义路径及原始查询串，例如 `/v1/users?tag=b&tag=a`。保留查询顺序、重复参数和百分号转义。域名和 URL 片段不参与签名。 |
| 6 | 请求体摘要 | 实际发送的正文字节的 SHA-256，以小写十六进制编码。空正文取零字节内容的摘要。 |

将 HMAC-SHA256 的完整结果编码为 64 位小写十六进制，放入 `X-Signature`。JSON 序列化一次，签名和发送共用同一份字节。调用方主动重试时，生成新的 nonce，按本次的时间戳、请求目标及正文重新签名。

## Go

每个下游在初始化时创建一次客户端，后续可并发复用。每次调用通过 `client.Request(ctx)` 创建请求。签名在 Resty 完成路径和正文序列化后写入，业务可直接传 JSON 对象或原始字节。

```go
import (
    "context"

    "github.com/bpcoder16/baize/service/appauth/appsign"
    "github.com/bpcoder16/pixiu/infra/httpcall"
    "github.com/go-resty/resty/v2"
)

func newProfilesClient(baseURL, appID, appSecret string) (*httpcall.Client, error) {
    return appsign.NewClient("profiles", appID, appSecret, httpcall.OptResty(func(c *resty.Client) {
        c.SetBaseURL(baseURL)
    }))
}

func postUsers(ctx context.Context, client *httpcall.Client, ids []int64) (*resty.Response, error) {
    return client.Request(ctx).
        SetHeader("Content-Type", "application/json").
        SetBody(map[string][]int64{"userIds": ids}).
        Post("/v1/users")
}
```

`appsign.NewClient` 接受 Pixiu 的 `httpcall.Option`。Resty 用 `BaseURL` 解析相对路径，也可以直接传入绝对 URL。签名客户端使用 Resty 的 `PreRequestHook`，并将 3xx 响应直接返回给调用方。

Resty 默认关闭自动重试。启用后，每轮 Resty 尝试都会执行签名钩子，生成新的 nonce。要重发相同正文，请传入可重复序列化的值或 `[]byte`。Go 的 HTTP Transport 在复用连接遇到特定网络故障时，也可能透明重发 GET 等请求，此时沿用原签名。如果服务端已占用 nonce 但响应丢失，重发可能收到 401。

写操作需要重试时，同一笔业务在各次尝试中应使用相同的业务幂等键，例如将订单请求号放入已签名的正文。服务端应按该键去重，并将去重记录与业务变更原子提交。这样，首次操作成功但响应丢失时，使用新 nonce 的重试也只产生一次业务效果。

## Python 3

下面的函数只使用标准库。可选的 `timestamp` 和 `nonce` 供离线测试固定输入，正常调用由函数生成。

```python
import hashlib
import hmac
import secrets
import time

def signed_headers(app_id, secret_hex, method, target, body, *, timestamp=None, nonce=None):
    timestamp = str(int(time.time())) if timestamp is None else timestamp
    nonce = secrets.token_hex(16) if nonce is None else nonce
    digest = hashlib.sha256(body).hexdigest()
    message = "\n".join((app_id, timestamp, nonce, method, target, digest)).encode("utf-8")
    signature = hmac.digest(bytes.fromhex(secret_hex), message, "sha256").hex()
    return {
        "X-App-Id": app_id,
        "X-Timestamp": timestamp,
        "X-Nonce": nonce,
        "X-Signature": signature,
    }
```

```python
import http.client
import json
import os

target = "/v1/users"
body = json.dumps({"userIds": [42]}, separators=(",", ":")).encode("utf-8")
headers = signed_headers(os.environ["APP_ID"], os.environ["APP_SECRET"], "POST", target, body)
headers["Content-Type"] = "application/json"

# USER_SERVICE_HOST 是部署提供的主机名，可包含端口。
conn = http.client.HTTPSConnection(os.environ["USER_SERVICE_HOST"], timeout=5)
try:
    conn.request("POST", target, body=body, headers=headers)
    response = conn.getresponse()
    print(response.status, response.read().decode("utf-8"))
finally:
    conn.close()
```

## 服务端接入

将[服务端模板](../../conf.example/baize.appauth.yaml)复制为入口配置同目录的 `baize.appauth.yaml`。在 `clients[].appSecret` 中直接填写分配给该调用方的 64 位十六进制密钥，用引号将其写为 YAML 字符串。用 `permissions` 声明该 AppID 可调用的权限。各副本加载同一份部署配置。配置在启动时生效，`clients: []` 会停用所有调用方。

在宿主的启动清单中注册 Redis 和准备步骤。已有同名 Redis 资源的服务可直接用该实例名构造 `appauthboot.Prepare`。在对应的 `redis.<实例名>.yaml` 中设置 `maxRetries: -1` 关闭命令重试。若启用了重试，appauth 会在装配时返回错误。

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{redisboot.Instances("cache")},
    Prepare:   []bootstrap.Step{appauthboot.Prepare("cache")},
}
```

`bootstrap`、`bootstrap/redisboot` 和 `bootstrap/appauthboot` 均位于 `github.com/bpcoder16/baize` 下。宿主自行挂载路由，用 `middleware/ginappauth` 指定每个操作需要的权限，用 `service/appauth` 读取已验证的调用方身份。

每条请求链只挂一次 `ginappauth.Require`。组内共用权限时挂在路由组上，各路由权限不同时分别挂在具体路由上。重复挂载会将同一请求的 nonce 判为重放，默认返回 401。

```go
router.POST("/v1/users", ginappauth.Require("users.read", nil), func(c *gin.Context) {
    appID, _ := appauth.AppIDFromContext(c.Request.Context())
    // 业务 handler 可以继续读取原始正文或调用 ShouldBindJSON。
    c.JSON(http.StatusOK, gin.H{"caller": appID})
})
```

服务端接受时间戳与服务端时间相差不超过 2 分钟的签名（含边界）。验签与权限检查通过后，通过 Redis 的原子 SET NX 写入 `baize:appauth:v1:<AppName>:<AppID>:<nonce>`。命名空间取自 `env.appName`。debug、test 和 release 通过独立的 Redis 实例或 DB 隔离。同一环境内，同一服务的各副本使用相同的应用名和 Redis DB，并同步时钟。nonce 记录从首次接收起保留 241 秒，覆盖允许提前到达的签名。防重放依赖这些记录在有效期内保留；部署时应为这部分键预留容量。验签中间件应安装在改写 URL 或消耗正文的中间件之前。

启用 Gin 访问日志时，受保护路由的 `HTTPRequest` 记录包含 `appauth_permission`（路由要求的权限）和 `appauth_result`（鉴权结果）。放行时结果为 `allowed`，并记录已验证的 `app_id`。拒绝时省略 `app_id`，结果为 `missing_credentials`、`invalid_credentials`、`forbidden`、`body_too_large` 或 `unavailable`。宿主自定义的 HTTP 状态单独记录在 `status` 字段。访问日志沿用 `gin.yaml` 的 `disableAccessLog` 开关，默认的请求和响应详情配置即可记录上述字段。

默认响应如下。宿主可通过 `ginappauth.Require(permission, writeFailure)` 自定义中止请求后的错误记录、HTTP 状态及业务码。

| 条件 | 默认 HTTP 状态 | 错误标识 |
| --- | --- | --- |
| 缺少签名，或凭证错误、时间超窗、nonce 重复 | 401 | `httpauth.ErrMissingCredentials` 或 `httpauth.ErrInvalidCredentials` |
| 凭证有效，缺少路由权限 | 403 | `appauth.ErrForbidden` |
| 原始正文超过 `maxBodyBytes`（默认 1 MiB） | 413 | `appauth.ErrBodyTooLarge` |
| 验证器未就绪或 Redis 操作失败 | 503 | `appauth.ErrUnavailable` |

## 离线核对

下面三组输入和预期签名使用同一组公开测试凭证。运行上面的 `signed_headers` 函数定义和以下片段，即可离线核对签名。

```python
app_id = "order-service"
secret_hex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
timestamp = "1700000000"
nonce = "00112233445566778899aabbccddeeff"

# 每组依次为名称、请求目标、原始正文和预期签名，HTTP 方法均为 POST。
cases = [
    (
        "users", "/v1/users", '{"userIds":[42]}',
        "d90a352468c80146ea60beaf15ddec71bd5ce1defe5bf5632cec01c0faf78a4c",
    ),
    (
        "escaped-query", "/v1/users?source=a%2Fb&x=1", '{"name":"中文"}',
        "6804438182d0ee6d56bb1d722e119f4ada237ccc37cfbc0754033e798d92de25",
    ),
    (
        "empty-body", "/v1/users", '',
        "b36988c8a3b761b363c7c479afebc4c1f77cf401719e1238be82ae25e43a09cb",
    ),
]

for name, target, body, expected in cases:
    headers = signed_headers(
        app_id, secret_hex, "POST", target, body.encode("utf-8"),
        timestamp=timestamp, nonce=nonce,
    )
    assert headers["X-Signature"] == expected, name
```
