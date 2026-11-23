<p align="center">
  <img src="assets/baize-logo.png" width="160" alt="白泽 Baize 吉祥物，月白身、玉青鬃、额生双角">
</p>

<h1 align="center">白泽 · Baize</h1>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.27.3%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.27.3+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="许可证 Apache-2.0"></a>
</p>

白泽（bái zé，Baize）是基于 Pixiu 的 Go 业务复用库，在同一处维护多个应用共用的启动流程和业务代码。Pixiu 提供日志、数据库客户端、HTTP、Cron、NATS 等技术能力，业务项目保留自己的入口、部署配置和产品规则。[Pixiu 项目](https://github.com/bpcoder16/pixiu)

项目以传说中能言、通晓万物的神兽白泽命名。吉祥物月白身、玉青鬃，与 Pixiu、Qilin 同为昂首坐姿。

## 项目状态

当前支持按清单初始化资源、执行业务准备、构造和运行宿主服务，提供 MySQL、PostgreSQL、ClickHouse、SQLite、Redis、NATS、Elasticsearch、阿里云 OSS、STS 和 ES 管理端命名实例适配器。服务装配支持 Realtime WebSocket、Gin HTTP、Cron，以及按 NATS 实例声明的 Core 和 JetStream 消费者。Baize 提供固定租期的单节点 Redis 锁，以及 JetStream Outbox 的事务登记、投递与补偿。

## 业务编号与展示

- `bizid.Generate("PD")` 生成带前缀的业务编号，沿用应用时区、毫秒时间和四位随机尾号。需要唯一编号的表应设置唯一索引并处理冲突。[业务编号文档](bizid/doc.go)
- `display.FormatTime(t)` 按应用时区输出 RFC3339，空时间返回空字符串。`display.MaskName("张三")` 返回 `张*`。[展示规则文档](display/doc.go)

## 初始化一个宿主

将 [app.yaml 模板](conf.example/app.yaml) 复制到宿主的 `conf/app.yaml`。最小入口如下。

```go
package main

import (
    "os"

    "github.com/bpcoder16/baize/bootstrap"
)

func main() {
    os.Exit(bootstrap.RunCLI(bootstrap.Plan{}))
}
```

在宿主目录运行 `go run .`，白泽读取配置并初始化环境、时区和日志，写入“白泽初始化完成”后关闭日志并退出。`go run . --conf /etc/cards/staging.yaml` 可指定其他文件，短参数为 `-c`，`go run . -h` 显示帮助。

环境在每个进程内只发布一次。配置检查工具可调用 `appconfig.Load(path)`。相对路径、兼容字段和初始化边界见[包说明](appconfig/doc.go)。

## 按需接入 MySQL

在需要数据库的入口导入 `github.com/bpcoder16/baize/bootstrap/mysqlboot`，把 [MySQL 模板](conf.example/mysql.default.yaml) 复制为入口配置同目录下的 `mysql.orders.yaml`，填入连接参数。

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("orders"),
    },
    Prepare: []bootstrap.Step{
        {
            Name: "check-database",
            Init: func(ctx context.Context, _ string, _ *lifecycle.Stack) error {
                db := mysqlx.Named("orders").MasterDB(ctx)
                return db.Exec("SELECT 1").Error
            },
        },
    },
}
return bootstrap.Run(ctx, path, plan)
```

多实例在同一次调用中声明，如 `mysqlboot.Instances("orders", "audit")`。白泽先解析这一组的全部文件，再按顺序建连。这组实例作为一个资源步骤。`Run` 在每个资源步骤开始前检查取消。组内建连期间收到取消时，当前组会继续到全部打开或首次出错，随后回收已登记的资源。每个名字都用 `mysqlx.Named(name)` 访问，包括名为 `default` 的实例。只使用基础入口的程序可以独立构建，MySQL 驱动仅随显式依赖它的入口链接。

宿主自己的初始化可以作为下一项 `bootstrap.Resource{Name: "billing", Init: app.openBilling}` 加入 `Plan.Resources`。回调签名为 `func(configDir string, resources *lifecycle.Stack) error`，执行时可借用前面步骤已就绪的句柄。后续同样从命名 YAML 创建客户端的适配器可复用 `bootstrap.NamedInstances`，提供打开单个实例和统一关闭的函数即可。每类命名客户端由一个步骤统一管理，借用方无需再登记关闭。

## 按需接入 PostgreSQL、ClickHouse 和 SQLite

从 `github.com/bpcoder16/baize/bootstrap/` 导入所需数据库的装配包，将实例声明加入 `Plan.Resources`。实例文件放在入口配置同目录，每种数据库可一次声明多个名称，例如 `postgresqlboot.Instances("orders", "audit")`。

| 数据库 | 启动声明 | 业务中取得 GORM 会话 | 示例配置 |
| --- | --- | --- | --- |
| PostgreSQL | `postgresqlboot.Instances("default")` | `postgresqlx.Named("default").MasterDB(ctx)` | [postgresql.default.yaml](conf.example/postgresql.default.yaml) |
| ClickHouse | `clickhouseboot.Instances("default")` | `clickhousex.Named("default").MasterDB(ctx)` | [clickhouse.default.yaml](conf.example/clickhouse.default.yaml) |
| SQLite | `sqliteboot.Instances("default")` | `sqlitex.Named("default").DB(ctx)` | [sqlite.default.yaml](conf.example/sqlite.default.yaml) |

业务客户端分别位于 Pixiu 的 `infra/postgresqlx`、`infra/clickhousex`、`infra/sqlitex`。PostgreSQL 沿用 Pixiu 的 `postgresql.<实例名>.yaml`，包括证书路径及 SQL 日志默认值。PostgreSQL 和 ClickHouse 都通过 `SlaveDB(ctx)` 轮询从库，省略从库时回到主库。ClickHouse 可选择 Native 或 HTTP 协议，开启 TLS 时使用系统 CA 校验对应节点。

PostgreSQL 的 `sslRootCert`、`sslCert` 和 `sslKey` 填相对文件路径时，从进程工作目录解析。例如，在 `/srv/shop` 执行 `./shop --conf /etc/shop/app.yaml`，`sslRootCert: "certs/ca.pem"` 会读取 `/srv/shop/certs/ca.pem`。证书随配置部署在 `/etc/shop/certs/` 时，可直接填写绝对路径 `/etc/shop/certs/ca.pem`。

SQLite 使用 CGO 驱动，构建该入口时需要启用 CGO 并安装 C 编译器。示例用 `:memory:` 创建单连接内存库，进程退出后数据释放。需要持久化时填写文件路径或 `file:` URI，并提前创建父目录。相对路径以进程工作目录为准。

## 在 Prepare 中迁移 MySQL 和 PostgreSQL

为需要迁移的实例声明步骤，使用已在 `Plan.Resources` 打开的主库。宿主提供 `[]schemamigrate.Table`，可按数据库传入各自的模型和种子 SQL。以下示例仅迁移表结构。

```go
Prepare: []bootstrap.Step{
    mysqlboot.PrepareMigration("default", mysqlTables, mysqlboot.SeedOptions{}),
    postgresqlboot.PrepareMigration("default", pgTables, postgresqlboot.SeedOptions{}),
},
```

声明步骤后，在入口配置同目录提供 `mysql-migrate.<实例名>.yaml` 或 `postgresql-migrate.<实例名>.yaml`。两份文件独立生效，`enabled` 默认关闭，同时控制迁表和种子。迁移 Biucards 旧配置时，将 `migrate.default.yaml` 改名为 `mysql-migrate.default.yaml`。[MySQL 模板](conf.example/mysql-migrate.default.yaml) · [PostgreSQL 模板](conf.example/postgresql-migrate.default.yaml)

启用后，Pixiu 按清单迁表，成功后再执行种子。通过 `SeedOptions{Files: files, Seeds: seeds}` 提供内嵌 SQL 和种子清单。每个实例的 YAML `timeout` 是迁表和种子共用的总时限，默认 `5m`。种子使用迁表后剩余的时间，启动 context 提前取消时，两阶段均响应取消。任一步失败会中止启动，已经完成的表或已提交的种子项可能保留。

每次部署只启用一个执行迁移的进程。种子按声明范围比较行数，数量一致时保留数据，数量不同时在单项事务中删除该范围并重新导入。`Charset` 和 `Collation` 仅用于 MySQL，种子 SQL 须适用于目标数据库。

## 按需接入 Redis

在 `Plan.Resources` 中加入 `redisboot.Instances("default")`，把 [Redis 模板](conf.example/redis.default.yaml) 复制到入口配置同目录，填入单节点 Redis 的地址和凭据。一个入口使用多个 Redis 时，在同一次调用中列出所有实例名，并为每个实例提供 `redis.<name>.yaml`。

```go
// 装配使用 github.com/bpcoder16/baize/bootstrap/redisboot。
// 锁配置使用 github.com/bpcoder16/baize/service/lockx/redislock。
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("default"),
        redisboot.Instances("default"),
    },
    Prepare: []bootstrap.Step{
        redisboot.PrepareLock("default", "default", redislock.Config{}),
    },
}
```

Redis 初始化时执行 `PING`。业务准备和服务中通过 `redisx.Named("default").Client()` 借用客户端，执行命令时传入调用方的 `ctx`。从 Biucards 迁移时可沿用原有 Redis YAML，将宿主封装中的 `redisx.Default()` 改为按名称取实例。示例按 MySQL、Redis 的顺序建连，Redis 启动失败时，白泽会关闭已打开的连接池，最后关闭日志。

`PrepareLock(name, redisName, cfg)` 在准备阶段复用 `redisName` 的连接池，该 Redis 实例须配置 `maxRetries: -1`。业务通过 `lockx.Named(name)` 取得执行器。

业务导入 `github.com/bpcoder16/baize/service/lockx`。例如，`lockx.Named("default").TryDoMany(ctx, keys, work)` 在取得整组锁后执行一次回调，竞争失败返回 `false, nil`。单键等待和单次尝试分别使用 `Do` 和 `TryDo`。零值配置的租期为 1 分钟，阻塞获取最多等待 5 秒，重试间隔以 50 毫秒为基准。[锁契约](service/lockx/doc.go) · [Redis 实现](service/lockx/redislock/doc.go)

从 Pixiu 迁移的宿主须一并切换锁配置、执行业务的入口、测试后端和注册代码。两个包各有进程级注册表，`PrepareLock("default", …)` 登记的执行器通过 Baize 的 `lockx.Named("default")` 取得。

设置 `BAIZE_REDIS_TEST_ADDR` 后运行 `go test ./service/lockx/...` 可验证真实 Redis 上的互斥和租期。另设 `BAIZE_REDIS_TEST_ACL=1` 可验证批量锁遇到权限错误时的清理行为，该用例会在测试实例上创建并删除临时 ACL 用户。

## 按需接入 NATS

在 `Plan.Resources` 中加入 `natsboot.Instances("default")`，将 [NATS 配置模板](conf.example/nats.default.yaml) 放到入口配置同目录。多个逻辑实例在一次调用中声明，例如 `natsboot.Instances("default", "events")`，每个实例读取自己的 `nats.<实例名>.yaml`。同一集群的多个节点填在同一份文件的 `urls` 中。

```go
// 装配包为 github.com/bpcoder16/baize/bootstrap/natsboot。
// 在资源就绪后的业务代码中，通过 github.com/bpcoder16/pixiu/infra/natsx 借用实例。
client := natsx.Named("default")
return client.Publish(ctx, "orders.created", data)
```

Core NATS 和 JetStream 共用该连接。向已有 Stream 发布并等待服务端确认时可用 `client.PublishJetStream(ctx, subject, data, messageID)`。客户端初始化只建立连接并创建 JetStream 上下文，使用 JetStream 发布时需要服务端启用 JetStream 并配置匹配主题的 Stream。客户端在资源栈关闭时统一排空。

需要消费消息时，在 `Plan.Services` 中声明对应实例的服务。参数顺序与 Biucards 一致，分别是实例名、JetStream 注册表和 Core 注册表。每类可传 `nil` 表示无 Handler。

```go
natsboot.ConsumerService(
    "default",
    natsrouter.JetStreamRegistry,
    natsrouter.CoreRegistry,
)
```

每个实例只声明一个消费服务。`Build` 严格读取入口配置同目录的 `nats-register.<实例名>.yaml`，将 `streams[].consumers[].handler` 和 `coreConsumers[].handler` 分别绑定到对应注册表。注册名区分大小写。`Build` 预检整份声明，成功后由 `Run` 注册远端资源并启动订阅。Stream 支持 `create`、`upsert`、`existing`，Durable 可独立选择 `create` 或 `upsert`。命名空间和日志名取自连接配置。[订阅配置模板](conf.example/nats-register.default.yaml)

设置 `queue` 时，`subscribers` 是每个实例的同组订阅数，默认 1，显式填写须为正整数。停机可中断正在等待的 Core 注册确认。

在 `disabledServices` 加入 `"nats-consumer:default"` 可跳过该实例的消费装配，加入 `"nats-consumer"` 可禁用全部消费者。发布方仍可使用已声明的客户端。停机时先停收并排空在途消息，再由资源栈关闭连接。

## 按需接入 Elasticsearch

按集群主版本导入 `bootstrap/elasticsearchboot/v7`、`v8` 或 `v9`。例如，连接 Elasticsearch 8 时，入口只需导入对应的装配包：

```go
import esv8 "github.com/bpcoder16/baize/bootstrap/elasticsearchboot/v8"
```

将 `esv8.Instances("default")` 加入 `Plan.Resources`，并将 [Elasticsearch 模板](conf.example/elasticsearch.default.yaml) 复制为入口配置同目录下的 `elasticsearch.default.yaml`。同一集群的多个节点写入 `addresses`。启动时会通过 `GET /` 校验集群主版本。迁移旧配置时删除 `version` 字段，保留连接参数。`caCertFile` 的相对路径从该实例 YAML 所在目录解析。

连接同一主版本的多个集群可用 `esv8.Instances("default", "archive")`。如果 `archive` 使用 v7，则同时导入 v7 和 v8，在一个资源步骤中声明 `elasticsearchboot.Instances(esv8.Named("default"), esv7.Named("archive"))`。每个实例各有一份 `elasticsearch.<实例名>.yaml`。

```go
// 资源就绪后，通过 github.com/bpcoder16/pixiu/infra/elasticsearchx 按名称借用客户端。
client := elasticsearchx.Named("default")
count, err := client.Count(ctx, "products", query)
```

## 按需接入阿里云 OSS、STS 和 ES 管理端

从 `github.com/bpcoder16/baize/bootstrap/` 按需导入 `aliyunossboot`、`aliyunstsboot` 或 `aliyunesboot`。三个装配包可独立使用。

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        aliyunossboot.Instances("default"),
        aliyunstsboot.Instances("oss"),
        aliyunesboot.Instances("search-control"),
    },
}
```

| 客户端 | 入口配置同目录下的实例文件 | 业务中获取客户端 |
| --- | --- | --- |
| OSS | [aliyunossx.default.yaml](conf.example/aliyunossx.default.yaml) | `ossx.Named("default")` |
| STS | [aliyunstsx.oss.yaml](conf.example/aliyunstsx.oss.yaml) | `stsx.Named("oss")` |
| ES 管理端 | [aliyunesx.search-control.yaml](conf.example/aliyunesx.search-control.yaml) | `esx.Named("search-control")` |

业务端从 `github.com/bpcoder16/pixiu/infra/aliyun/` 下对应的 `ossx`、`stsx` 或 `esx` 包按名称取得客户端。每个 OSS 实例绑定一个 Bucket。多个桶可一次声明，例如 `aliyunossboot.Instances("geca-public", "geca-private")`，每个实例各配一份 YAML。已有 Pixiu 或 Biucards 项目可以沿用同名实例文件。

启动时在本地构造客户端。访问 OSS 时使用该实例配置的凭据。需要签发临时凭据时，业务调用 `stsx.Named("oss").AssumeRole(ctx, req)`，在请求中指定角色 ARN、有效期和可选策略。

每个 ES 管理端名称绑定配置中的一个阿里云 `instanceID`。业务可调用 `esx.Named("search-control").DescribeInstance(ctx)` 查询该实例，也可通过同一客户端管理同义词词典。索引和文档读写使用上一节的 Elasticsearch 数据客户端。

## Realtime 会话与推送

`service/realtime` 基于 Pixiu 的 `infra/websocketx` 管理会话，按 `scene/action` 路由请求，并按用户、设备或 Topic 推送。宿主通过 `realtime.NewServer` 注入认证与路由。将 `realtimeboot.Service("app", newAppServer)` 加入 `Plan.Services`，由白泽统一管理独立 HTTP 监听和已升级的连接。[调用示例](service/realtime/doc.go)

服务启用时读取入口配置同目录的 `realtime.app.yaml`，工厂返回全新实例，登记后业务可用 `realtime.Named("app")` 推送。默认示例的升级路径为 `/ws/app`，`GET /ready/ws` 在服务就绪时返回 204。在 `disabledServices` 中填 `realtime` 可禁用整组，填 `realtime:app` 可禁用单个实例。[监听配置](conf.example/realtime.app.yaml)

客户端握手时提供 `baize.realtime.v1.json` 或 `baize.realtime.v1.protobuf`。Go 宿主从 `github.com/bpcoder16/baize/service/realtime/protocol` 导入公共信封类型，Protobuf 包名为 `baize.realtime.v1`。[公共信封](service/realtime/protocol/realtime.proto)

## 业务准备与常驻服务

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("orders"),
    },
    Prepare: []bootstrap.Step{
        mysqlboot.PrepareMigration("orders", app.Tables(), mysqlboot.SeedOptions{}),
    },
    Services: []bootstrap.Service{
        httpboot.Gin(
            router.Register,
            ginx.OpenAPIModule{Name: "app", Title: "App"},
            ginx.OpenAPIModule{Name: "admin", Title: "Admin"},
        ),
        cronboot.Service(cronrouter.Registry),
    },
}
return bootstrap.Run(ctx, path, plan)
```

Baize 内置准备步骤的 `Step.Name` 统一使用 `baize.` 前缀，例如 `baize.mysql-default-migrate` 和 `baize.jetstream-outbox`。

资源就绪后，先按声明顺序调用所有启用服务的 `BeforePrepare`，再依次执行 `Plan.Prepare`。全部步骤成功后，按相同的服务顺序调用 `AfterPrepare`，然后开始 `Build`。两个钩子均可省略，签名为 `func(context.Context) (context.Context, error)`。成功时将非空上下文传给后续钩子和阶段。任一钩子返回错误时立即停止启动，回收已登记的资源。返回的错误包含服务名和钩子名，调用方可用 `errors.Is` 或 `errors.As` 检查原始错误。Cron 通过 `BeforePrepare` 提供内置任务的登记入口。

`Step.Init(ctx, configDir, resources)` 在资源就绪后按顺序执行。宿主可在这里迁移、加载业务配置、绑定处理函数。新增资源立即登记到栈。已有的资源初始化函数沿用 `Init(configDir, resources)` 签名，可自行设置连接超时。

Gin 宿主导入 `github.com/bpcoder16/baize/bootstrap/httpboot`，通过 `router.Register(*gin.Engine)` 挂载自己的路由。Demo 在 `internal/http/router` 按端注册路由，在 `internal/http/handler` 编写处理函数和各端的 Swag 注释。将两份模板放在入口配置同目录，`gin.yaml` 控制可信代理和访问日志，`http.yaml` 控制监听地址与读写超时。[Gin 模板](conf.example/gin.yaml) · [HTTP 模板](conf.example/http.yaml)

`gin.yaml` 可选的 `cors` 列表按路径前缀配置浏览器跨域访问。省略或留空时默认关闭；填写后由 `httpboot.Gin` 在构造 Engine 时挂载 Pixiu 中间件，业务路由只需注册实际方法。每条规则必须列举来源和方法，非法或重叠规则会在启动时报告配置文件和规则位置。

在 debug/test 模式下，宿主需提前导入生成的 Swag 文档包，每个 `--instanceName` 与模块的 `Name` 对应。`/docs` 默认展示首个模块，`/docs/<Name>` 和 `/openapi/<Name>.json` 分别提供页面和 JSON。省略模块时使用 Swag 默认实例，JSON 位于 `/openapi/doc.json`。release 模式跳过文档注册。

Cron 宿主导入 `github.com/bpcoder16/baize/bootstrap/cronboot`，通过 `cronx.MustNewHandlerRegistry` 登记签名为 `func(context.Context) error` 的包级函数。每个进程声明一个 Cron 服务，`cron.yaml` 的 `jobs` 按名称选择任务，名称区分大小写。每项可选固定间隔 `every`、六字段表达式 `cron`（从秒开始，使用应用时区）或随机间隔 `randomEvery`。固定间隔与随机下限至少为 `100ms`，随机上限须大于下限。[Cron 模板](conf.example/cron.yaml)

设置 `jobs: []` 会停用 YAML 中配置的任务。准备步骤通过 `cronboot.AddJob` 登记的任务仍会运行，例如 JetStream Outbox 补偿。使用 `outboxboot.PrepareJetStream` 的宿主须启用 Cron，供该步骤登记补偿任务。

多副本部署时，可在 `lock.redisName` 引用 `redisboot.Instances` 已声明的实例。同组副本使用相同的 Redis 数据库、`keyPrefix` 和任务名，未获锁的副本跳过本轮。开启锁时每个任务须配置正的 `timeout`，超时会取消 Handler 的 context，任务实际返回后才解锁。

`Service.Build(ctx, configDir)` 返回 `(apprun.Service, error)`。服务的名称以清单为准。全部构造成功后，Pixiu 并发运行服务。收到取消或任一服务退出时，先停收、排空全部服务，再逆序关闭资源和日志。服务回调支持在运行前停收，以便后续构造失败时回收。构造失败的步骤负责清理本次尚未移交的状态。

`disabledServices` 在构造前按类型或完整名称过滤，允许的服务类型由 `constant.IsServiceType` 定义。没有声明服务时，业务准备完成即退出。声明的服务全部禁用时，返回 `apprun.ErrNoServices` 并跳过资源初始化。

`serviceShutdownTimeout` 控制全部服务共用的停收及排空时限，默认 30 秒。超时后 Pixiu 通知尚未完成的服务取消，随后开始独立计算 `resourceCloseTimeout`（默认 30 秒）。单个 NATS 客户端的 `closeTimeout` 默认 20 秒，计入资源阶段。多个 NATS 实例逐个关闭，部署时应为其他客户端和日志预留回收时间。`app.yaml` 的两个停机超时字段省略或写 `0s` 时使用默认值，负值报错。

应用和实例配置中的超时建议写明单位，例如 `"30s"` 或 `"500ms"`。YAML 整数按纳秒解析，`resourceCloseTimeout: 30` 表示 30 纳秒，要设置 30 秒应写 `resourceCloseTimeout: "30s"`。

`bootstrap.RunCLI(plan)` 解析命令行并接管 SIGINT、SIGTERM。宿主已有 context 时调用 `bootstrap.Run(ctx, path, plan)`。

## 目标目录

通用业务按调用方、业务流程和持久化分层。宿主在启动时导入对应的 Boot 包，处理业务时调用 Service。

| 目录 | Outbox 中的职责 |
| --- | --- |
| `constant/`、`error/outbox/` | 定义数据库方言、发布类型、投递状态和共享错误。 |
| `model/entity/<数据库>/outbox/` | 分别定义 MySQL 和 PostgreSQL 的表结构与索引。 |
| `model/outbox/` | 定义事务任务、记录、投递快照及 JetStream 消息正文。 |
| `repository/outbox/` | 选择实体、迁表、事务插入、按类型扫描和清理、持锁读取与回写。各业务仓储可复用 `repository/internal/dbinit` 的会话校验和命名迁移锁。 |
| `service/outbox/` | 提供业务入口，按类型登记 Worker，执行投递与补偿。 |
| `service/outbox/nats/` | 按实例发布至 JetStream，以有效 PubAck 确认发送成功。 |
| `bootstrap/outboxboot/` | 借用命名资源，初始化仓储和 Worker，将按类型执行的补偿任务接入宿主 Cron。 |

## JetStream Outbox

在 `Plan.Prepare` 中声明主库、发布实例和补偿调度。以下配置每 10 秒触发一次，每轮最多运行 1 分钟。数据库也可选 `constant.PostgreSQL`。

```go
outboxboot.PrepareJetStream(outboxboot.JetStreamConfig{
    Backend:         constant.MySQL,
    DatabaseName:    "default",
    ResourceNames:   []string{"events", "audit"},
    CronTaskEvery:   10 * time.Second,
    CronTaskTimeout: time.Minute,
})
```

Outbox 沿用所选 MySQL 或 PostgreSQL 实例的迁移配置。首次部署或升级时，由一个进程设置 `enabled: true`，在数据库会话锁内创建或更新 `baize_outbox`。迁移完成后，普通副本设置 `enabled: false`，直接使用已有表。空表可正常启动，缺表会在 Prepare 阶段报错。

宿主须启用 Cron。Prepare 验证名单内的 NATS 实例可访问 JetStream，打开任务表后登记 `nats_jetstream` Worker 和补偿任务。同进程每种 Handler 类型最多登记一个 Worker。同库同 schema 的 Worker 可以共用该表，共用同一张表和类型的副本配置相同的发布名单。

滚动新增发布目标时，先将目标追加到所有投递副本的名单，全部生效后再向它登记任务。下线目标时，先让所有生产端停止向它登记（包括通过名单首项选取默认目标的调用），待在途业务事务结束、该目标的 `pending` 数量归零后，再从各副本移除。副本遇到名单外的目标会将任务标为 `failed`。

在写业务数据的同一个事务里调用 `outbox.EnqueueJetStreamTx(tx, outbox.JetStreamMessage{...})`。事务须从所选主库的同一个连接池开启，并使用配置的数据库和 schema。传入同方言的其他连接池返回 `ErrInvalidSession`。消息要求合法的 `Subject`、显式的 `Data` 和 `Header`，以及非零的 `ExpiresAt`。允许空数据和空 Header。Header 名须为合法 HTTP token，例如 `X-Trace-ID`，非法名称在入队时返回 `ErrInvalidArgument`。`ResourceName` 省略时使用名单首项。实际目标名称及 `handler_type=nats_jetstream` 随任务一起入库。事务上下文中的 `logId` 仅用于调试，缺失或超出两种数据库共同支持的文本范围时留空，业务事务可正常提交。

最外层业务事务提交后，可调用 `outbox.SendJetStream(ctx, id)` 或 `outbox.SendAsyncJetStream(id)`。通用入口 `outbox.Send(ctx, handlerType, id)` 和 `outbox.SendAsync(handlerType, id)` 接收显式类型。重试沿用任务 ID 作为 `Nats-Msg-Id`，消费端仍需幂等。

补偿任务 `baize.outbox.retry.nats_jetstream` 每页扫描 200 条该类型的待发记录。每次扫描先固定最大待发 ID。整轮超时后，同一 Worker 下次从最后尝试的 ID 之后继续。扫完上界后再从头重试，期间新增的任务也在下一遍处理。进度保存在 Worker 内存中，进程重启后从头扫描。多副本共用 Cron 的锁配置与任务名。

临时发布错误留在 `pending`，过期或目标已移出名单的任务进入 `failed`。清理窗口内只清理该类型的终态。指定类型未登记时，同步入口返回 `ErrHandlerNotRegistered`，异步入口在调用方 panic。任务类型与 Worker 绑定的类型不一致时，发送返回或记录 `ErrHandlerTypeMismatch`，任务保持原状。

单次发送的加锁、发布和回写共用一个 context。提交前取消时事务回滚并释放行锁，任务保留上次提交的状态、尝试次数和错误原因，后续补偿可以重试。

数据库集成测试通过 `BAIZE_OUTBOX_MYSQL_DSN` 和 `BAIZE_OUTBOX_POSTGRESQL_DSN` 连接测试实例。设置后，在仓库根目录运行 `go test ./...`。

## 许可证

[Apache License 2.0](LICENSE)
