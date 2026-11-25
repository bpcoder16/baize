<p align="center">
  <img src="assets/baize-logo.png" width="160" alt="白泽 Baize 吉祥物">
</p>

<h1 align="center">白泽 · Baize</h1>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.27.3%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.27.3+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="Apache License 2.0"></a>
</p>

**Baize 是基于 Pixiu 的 Go 业务复用库。** 如果你的多个应用都需要验证请求身份、确认文件上传，或在数据库事务提交后可靠地发送消息，可以直接接入对应的业务包。Baize 也提供统一的启动入口，让 HTTP 服务、定时任务和消息消费者共用一套配置、日志和停机流程。

Pixiu 提供数据库连接、WebSocket、NATS 等基础组件。Baize 在这些组件上实现业务流程，应用负责自己的路由、权限规则和部署配置。[Pixiu 项目](https://github.com/bpcoder16/pixiu)

## 可以用它做什么

| 需求 | Baize 提供的能力 | 从这里开始 |
| --- | --- | --- |
| 搭建 Go 服务 | 按清单初始化资源，运行 Gin HTTP、Cron、NATS 消费者或 WebSocket 服务，退出时排空请求并回收资源 | [快速开始](#快速开始) |
| 接入 HTTP 认证 | 应用提供令牌校验函数，Gin 路由选择必须登录或允许游客访问，下游从请求上下文读取身份 | [认证契约](httpauth/doc.go) · [Gin 用法](middleware/ginauth/doc.go) |
| 让客户端直传文件 | 按场景签发阿里云 OSS 临时凭据，核对对象、批量确认记录并生成访问地址，也支持服务端直接上传 | [OSS 接入指南](docs/oss-upload-integration.md) |
| 生成可转发的页面链接 | 用类型化请求生成加密链接，由宿主的 Gin 路由分发到对应 handler。在分享配置中按编号填写 Base64 密钥，保留旧密钥和页面版本可持续打开历史链接 | [分享接入与双向兼容](docs/share-design.md) · [轮换配置示例](conf.example/baize.share.yaml) |
| 可靠地发送业务事件 | 在写业务数据的同一事务中登记 Outbox 消息，提交后投递至 NATS JetStream，由 Cron 补偿待发任务 | [事务发件箱](#事务发件箱) |
| 向在线客户端推送 | 通过 WebSocket 按用户、设备或 Topic 推送，按 `scene/action` 路由请求，支持 JSON 和 Protobuf | [会话与推送示例](service/realtime/doc.go) |
| 避免多个任务同时处理同一资源 | 使用单节点 Redis 的固定租期锁，支持等待获取、单次尝试和一次获取多个键 | [锁的使用约定](service/lockx/doc.go) · [Redis 实现](service/lockx/redislock/doc.go) |

也可单独使用业务编号、时间格式化和姓名脱敏工具。例如 `display.MaskName("张三")` 返回 `张*`。业务编号带时间和随机尾号，需要唯一性时应在存储层设置唯一约束。[业务编号](bizid/doc.go) · [展示工具](display/doc.go)

## 快速开始

下面用 Go 1.27.3 或更高版本启动一个返回 `hello` 的 HTTP 服务，无需数据库或云账号。示例固定使用 `v0.1.4`。接入本页的 HTTP 认证功能时，请选用包含 `httpauth` 的后续标签或提交。

**1. 创建项目并复制监听配置。** 在终端执行以下命令。模板来自安装的模块，默认监听 `127.0.0.1:8888`。

```sh
mkdir hello-baize
cd hello-baize
go mod init example.com/hello
go get github.com/bpcoder16/baize/bootstrap/httpboot@v0.1.4
mkdir conf
baize_dir=$(go list -m -f '{{.Dir}}' github.com/bpcoder16/baize)
cp "$baize_dir/conf.example/gin.yaml" "$baize_dir/conf.example/http.yaml" conf/
```

**2. 创建 `conf/app.yaml`。** 本例选择 `release` 模式，只注册业务路由。`debug` 和 `test` 模式还会加载应用生成的 Swag 接口文档。[完整配置模板](conf.example/app.yaml)

```yaml
env:
  appName: "hello"
  runMode: "release"
  timeLocation: "Asia/Shanghai"
log:
  format: "json"
  hookStd: false
```

**3. 创建 `main.go` 并启动服务。**

```go
package main

import (
	"net/http"
	"os"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/httpboot"
	"github.com/gin-gonic/gin"
)

func routes(r *gin.Engine) {
	r.GET("/hello", func(c *gin.Context) {
		c.String(http.StatusOK, "hello\n")
	})
}

func main() {
	os.Exit(bootstrap.RunCLI(bootstrap.Plan{
		Services: []bootstrap.Service{httpboot.Gin(routes)},
	}))
}
```

运行 `go run .`，在另一个终端执行 `curl http://127.0.0.1:8888/hello`，应收到 `hello`。按 Ctrl+C 结束服务。默认入口配置是 `./conf/app.yaml`，也可用 `go run . --conf /path/to/app.yaml` 指定。

## 把组件接入自己的应用

`bootstrap.Plan` 描述一个进程要使用哪些组件。上面的 HTTP 服务只用到了 `Services`。需要数据库或业务组件时，可以按启动顺序补上前两个阶段。

| 阶段 | 放什么 | 例子 |
| --- | --- | --- |
| `Resources` | 进程共享的客户端，按声明顺序打开 | `mysqlboot.Instances("orders")` 读取 `mysql.orders.yaml`，业务通过 Pixiu 的 `mysqlx.Named("orders")` 借用连接 |
| `Prepare` | 依赖客户端的准备工作 | 迁移表结构，装配上传服务，或向 Cron 登记 Outbox 补偿任务 |
| `Services` | 需要持续运行的服务 | `httpboot.Gin(routes)`、`cronboot.Service(registry)`、`natsboot.ConsumerService(name, js, core)` |

启动代码从 `bootstrap/` 导入所需的装配包，业务代码按需调用 `service/oss`、`service/outbox` 等包。查找某项功能的数据结构和持久化实现时，分别看 `model/` 和 `repository/` 下的同名目录。

资源配置和服务配置都放在入口配置文件的同一目录。同类资源有多个实例时，在一次 `Instances("orders", "audit")` 调用中声明，每个实例各有一份配置。命令行程序用 `bootstrap.RunCLI(plan)`，已有 `context.Context` 的程序可直接调用 `bootstrap.Run(ctx, path, plan)`。

一次性任务可以只声明 `Resources` 和 `Prepare`，执行完成后自动退出。常驻服务收到停机信号时，先停收并排空，再逆序关闭资源，最后关闭日志。`app.yaml` 的 `disabledServices` 可以按类型或实例停用服务，例如 `nats-consumer:orders`。服务排空和资源关闭各有独立时限，默认各 30 秒。[应用配置](appconfig/doc.go) · [启动清单](bootstrap/plan.go)

### 配置与适配器

| 组件 | 配置与接入 |
| --- | --- |
| 数据库 | [MySQL](conf.example/mysql.default.yaml) · [PostgreSQL](conf.example/postgresql.default.yaml) · [ClickHouse](conf.example/clickhouse.default.yaml) · [SQLite](conf.example/sqlite.default.yaml)。SQLite 使用 CGO 驱动，构建时需要 C 编译器。 |
| 迁表与种子数据 | MySQL 和 PostgreSQL 均可在 `Prepare` 阶段执行迁移。每次部署由一个进程开启迁移。使用种子数据时，指定范围内的行数与预期不符会触发该范围的删除与重新导入。[MySQL 迁移](conf.example/mysql-migrate.default.yaml) · [PostgreSQL 迁移](conf.example/postgresql-migrate.default.yaml) |
| Redis 与业务锁 | `redisboot.PrepareLock(name, redisName, cfg)` 借用已声明的 Redis 实例。用于锁的连接须配置 `maxRetries: -1`，业务通过 `lockx.Named(name)` 取得执行器。[Redis 配置](conf.example/redis.default.yaml) |
| NATS 与 JetStream | 一个命名连接可用于 Core 和 JetStream 发布。需要消费时，每个实例声明一个消费服务，在订阅配置中按名称绑定 Handler。[连接配置](conf.example/nats.default.yaml) · [订阅配置](conf.example/nats-register.default.yaml) |
| HTTP、Cron 与 WebSocket | HTTP 监听和 Gin 中间件分别配置。Cron 支持固定间隔、Cron 表达式和随机间隔，Realtime 使用独立监听地址。[HTTP](conf.example/http.yaml) · [Gin](conf.example/gin.yaml) · [Cron](conf.example/cron.yaml) · [Realtime](conf.example/realtime.app.yaml) |
| Elasticsearch | 按集群主版本导入 `bootstrap/elasticsearchboot/v7`、`v8` 或 `v9`，启动时校验版本。[连接配置](conf.example/elasticsearch.default.yaml) |
| 阿里云 | 按需声明对象存储、临时凭据或 Elasticsearch 管理端客户端。每个 OSS 实例绑定一个 Bucket。[OSS](conf.example/aliyunossx.default.yaml) · [STS](conf.example/aliyunstsx.oss.yaml) · [ES 管理端](conf.example/aliyunesx.search-control.yaml) |

### 事务发件箱

例如，创建订单后需要发布 `orders.created`。在写入订单的同一个数据库事务中调用 `outbox.EnqueueJetStreamTx`，事务回滚时消息也一起回滚。最外层事务提交后，可用 `outbox.SendJetStream` 主动投递，也可以交给 Cron 补偿。消费端应按业务事件做幂等处理。[事务内登记与提交后发送示例](service/outbox/doc.go)

下面的清单使用名为 `orders` 的 MySQL 实例和名为 `events` 的 NATS 实例，每 10 秒补偿一次。接入前配置好目标 Stream，在入口配置同目录准备 `mysql.orders.yaml`、`mysql-migrate.orders.yaml`、`nats.events.yaml` 和 `cron.yaml`。首次建表时在一个进程启用迁移。即使 `cron.yaml` 中的 `jobs` 为空，Outbox 在准备阶段登记的任务仍会运行。[装配参数](bootstrap/outboxboot/prepare.go)

```go
plan := bootstrap.Plan{
	Resources: []bootstrap.Resource{
		mysqlboot.Instances("orders"),
		natsboot.Instances("events"),
	},
	Prepare: []bootstrap.Step{
		outboxboot.PrepareJetStream(outboxboot.JetStreamConfig{
			Backend:         constant.MySQL,
			DatabaseName:    "orders",
			ResourceNames:   []string{"events"},
			CronTaskEvery:   10 * time.Second,
			CronTaskTimeout: time.Minute,
		}),
	},
	Services: []bootstrap.Service{cronboot.Service(nil)},
}
```

**一张 Outbox 表可以向多个 JetStream Stream 投递，同一 Stream 的 Outbox 消息必须来自同一张表。** Baize 使用表内任务 ID 作为 `Nats-Msg-Id`，同一个 Stream 下的不同 Subject 共用去重窗口。两张表生成相同 ID 时，后一条业务消息可能被当作重复消息确认。[JetStream 发布约定](service/outbox/nats/doc.go)

使用同一张表的副本应配置相同的发布目标。下线目标前，先停止向它登记消息，等该目标的待发任务清空后再移除。

### OSS 上传

需要应用内的文件上传时，使用 `ossboot.Prepare` 装配数据库、OSS 和 STS 客户端。应用校验用户身份及记录归属，`CreateToken` 按场景签发上传凭据。客户端直传后调用 `Confirm`，服务从主库读取记录、核对 OSS 对象并整批更新状态。需要写入后立即生成访问地址时，可使用 `DisplayURLMapOnMaster` 或 `OriginURLMapOnMaster`。[场景配置](conf.example/baize.oss.yaml) · [从授权到确认的完整流程](docs/oss-upload-integration.md)

## 开发与许可证

在仓库根目录运行 `go build ./...` 构建，运行 `go test ./...` 测试。需要在真实服务上验证时，将 `BAIZE_OUTBOX_MYSQL_DSN`、`BAIZE_OUTBOX_POSTGRESQL_DSN` 或 `BAIZE_REDIS_TEST_ADDR` 指向对应的测试实例。

Baize 使用 Apache License 2.0。[许可证全文](LICENSE)
