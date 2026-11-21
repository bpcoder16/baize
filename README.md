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

当前支持按清单初始化资源、执行业务准备、构造和运行宿主服务，提供 MySQL、PostgreSQL、ClickHouse、SQLite、Redis、NATS、Elasticsearch、阿里云 OSS 和 STS 命名实例适配器。服务运行和资源回收使用 Pixiu `v0.1.12`。

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

多实例在同一次调用中声明，如 `mysqlboot.Instances("orders", "audit")`。白泽先解析这一组的全部文件，再按顺序建连。每个名字都用 `mysqlx.Named(name)` 访问，包括名为 `default` 的实例。只使用基础入口的程序可以独立构建，MySQL 驱动仅随显式依赖它的入口链接。

宿主自己的初始化可以作为下一项 `bootstrap.Resource{Name: "billing", Init: app.openBilling}` 加入 `Plan.Resources`。回调签名为 `func(configDir string, resources *lifecycle.Stack) error`，执行时可借用前面步骤已就绪的句柄。后续同样从命名 YAML 创建客户端的适配器可复用 `bootstrap.NamedInstances`，提供打开单个实例和统一关闭的函数即可。每类命名客户端由一个步骤统一管理，借用方无需再登记关闭。

## 按需接入 PostgreSQL、ClickHouse 和 SQLite

从 `github.com/bpcoder16/baize/bootstrap/` 导入所需数据库的装配包，将实例声明加入 `Plan.Resources`。实例文件放在入口配置同目录，每种数据库可一次声明多个名称，例如 `postgresqlboot.Instances("orders", "audit")`。

| 数据库 | 启动声明 | 业务中取得 GORM 会话 | 示例配置 |
| --- | --- | --- | --- |
| PostgreSQL | `postgresqlboot.Instances("default")` | `postgresqlx.Named("default").MasterDB(ctx)` | [postgresql.default.yaml](conf.example/postgresql.default.yaml) |
| ClickHouse | `clickhouseboot.Instances("default")` | `clickhousex.Named("default").MasterDB(ctx)` | [clickhouse.default.yaml](conf.example/clickhouse.default.yaml) |
| SQLite | `sqliteboot.Instances("default")` | `sqlitex.Named("default").DB(ctx)` | [sqlite.default.yaml](conf.example/sqlite.default.yaml) |

业务客户端分别位于 Pixiu 的 `infra/postgresqlx`、`infra/clickhousex`、`infra/sqlitex`。PostgreSQL 沿用 Pixiu 的 `postgresql.<实例名>.yaml`，包括证书路径及 SQL 日志默认值。PostgreSQL 和 ClickHouse 都通过 `SlaveDB(ctx)` 轮询从库，省略从库时回到主库。ClickHouse 可选择 Native 或 HTTP 协议，开启 TLS 时使用系统 CA 校验对应节点。

SQLite 使用 CGO 驱动，构建该入口时需要启用 CGO 并安装 C 编译器。示例用 `:memory:` 创建单连接内存库，进程退出后数据释放。需要持久化时填写文件路径或 `file:` URI，并提前创建父目录。相对路径以进程工作目录为准。

## 按需接入 Redis

在 `Plan.Resources` 中加入 `redisboot.Instances("default")`，把 [Redis 模板](conf.example/redis.default.yaml) 复制到入口配置同目录，填入单节点 Redis 的地址和凭据。一个入口使用多个 Redis 时，在同一次调用中列出所有实例名，并为每个实例提供 `redis.<name>.yaml`。

```go
// 装配包为 github.com/bpcoder16/baize/bootstrap/redisboot。
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("default"),
        redisboot.Instances("default"),
    },
}
```

Redis 初始化时执行 `PING`。业务准备和服务中通过 `redisx.Named("default").Client()` 借用客户端，执行命令时传入调用方的 `ctx`。从 Biucards 迁移时可沿用原有 Redis YAML，将宿主封装中的 `redisx.Default()` 改为按名称取实例。示例按 MySQL、Redis 的顺序建连，Redis 启动失败时，白泽会关闭已打开的连接池，最后关闭日志。

## 按需接入 NATS

在 `Plan.Resources` 中加入 `natsboot.Instances("default")`，将 [NATS 配置模板](conf.example/nats.default.yaml) 放到入口配置同目录。多个逻辑实例在一次调用中声明，例如 `natsboot.Instances("default", "events")`，每个实例读取自己的 `nats.<实例名>.yaml`。同一集群的多个节点填在同一份文件的 `urls` 中。

```go
// 装配包为 github.com/bpcoder16/baize/bootstrap/natsboot。
// 在资源就绪后的业务代码中，通过 github.com/bpcoder16/pixiu/infra/natsx 借用实例。
client := natsx.Named("default")
return client.Publish(ctx, "orders.created", data)
```

Core NATS 和 JetStream 共用该连接。向已有 Stream 发布并等待服务端确认时可用 `client.PublishJetStream(ctx, subject, data, messageID)`。客户端初始化只建立连接并创建 JetStream 上下文，使用 JetStream 发布时需要服务端启用 JetStream 并配置匹配主题的 Stream。客户端在资源栈关闭时统一排空。

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

## 按需接入阿里云 OSS 和 STS

按需导入 `github.com/bpcoder16/baize/bootstrap/aliyunossboot` 和 `github.com/bpcoder16/baize/bootstrap/aliyunstsboot`。两个客户端可独立使用。

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        aliyunossboot.Instances("default"),
        aliyunstsboot.Instances("oss"),
    },
}
```

| 客户端 | 入口配置同目录下的实例文件 | 业务中获取客户端 |
| --- | --- | --- |
| OSS | [aliyunossx.default.yaml](conf.example/aliyunossx.default.yaml) | `ossx.Named("default")` |
| STS | [aliyunstsx.oss.yaml](conf.example/aliyunstsx.oss.yaml) | `stsx.Named("oss")` |

业务端从 `github.com/bpcoder16/pixiu/infra/aliyun/ossx` 或 `github.com/bpcoder16/pixiu/infra/aliyun/stsx` 按名称取得客户端。每个 OSS 实例绑定一个 Bucket。多个桶可一次声明，例如 `aliyunossboot.Instances("geca-public", "geca-private")`，每个实例各配一份 YAML。已有 Pixiu 或 Biucards 项目可以沿用同名实例文件。

启动时在本地构造客户端。访问 OSS 时使用该实例配置的凭据。需要签发临时凭据时，业务调用 `stsx.Named("oss").AssumeRole(ctx, req)`，在请求中指定角色 ARN、有效期和可选策略。

## 业务准备与常驻服务

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("orders"),
    },
    Prepare: []bootstrap.Step{
        {Name: "migrate", Init: app.migrate},
    },
    Services: []bootstrap.Service{
        {Name: "http", Build: app.buildHTTP},
    },
}
return bootstrap.Run(ctx, path, plan)
```

`Step.Init(ctx, configDir, resources)` 在资源就绪后按顺序执行。宿主可在这里迁移、加载业务配置、绑定处理函数。新增资源立即登记到栈。已有的资源初始化函数沿用 `Init(configDir, resources)` 签名，可自行设置连接超时。

`Service.Build(ctx, configDir)` 返回 `(apprun.Service, error)`。服务的名称以清单为准。全部构造成功后，Pixiu 并发运行服务。收到取消或任一服务退出时，先停收、排空全部服务，再逆序关闭资源和日志。服务回调支持在运行前停收，以便后续构造失败时回收。构造失败的步骤负责清理本次尚未移交的状态。

`disabledServices` 在构造前按类型或完整名称过滤，允许的服务类型由 `constant.IsServiceType` 定义。没有声明服务时，业务准备完成即退出。声明的服务全部禁用时，返回 `apprun.ErrNoServices` 并跳过资源初始化。

`serviceShutdownTimeout` 控制全部服务共用的停收及排空时限，默认 30 秒。超时后 Pixiu 通知尚未完成的服务取消，随后开始独立计算 `resourceCloseTimeout`（默认 20 秒）。两个字段均支持带单位的时间，省略或 `0s` 使用默认值，负值报错。

`bootstrap.RunCLI(plan)` 解析命令行并接管 SIGINT、SIGTERM。宿主已有 context 时调用 `bootstrap.Run(ctx, path, plan)`。

## 目标目录

目录沿用业务项目的职责分层。以 Outbox 为例，表模型放在 `model/entity/outbox/`，事务登记和状态回写放在 `repository/outbox/`，投递与补偿流程放在 `service/outbox/`，JetStream 发布和 Cron 入口分别放在 `natspublisher/`、`cronhandler/`。宿主可以按需导入各层的公开包。

`bootstrap/` 负责应用装配。需要装配的业务能力在这里提供统一入口，把分散在各层的实现接到宿主已有的表迁移、路由、任务和订阅清单。实际用到哪一层，就在哪一层下沉代码。

## 后续的 Outbox 接入设计

1. 在宿主启动清单中选择共享实例和业务能力，并登记项目自己的处理函数。例如，已有 MySQL 和 NATS 的应用只需增加一项 Outbox 声明，引用已有实例。
2. 在宿主配置中指定部署行为。Outbox 的表随宿主迁移策略准备，补偿任务通过 `cron.yaml` 启用。需要消费事件的应用在自己的 `nats-register.<实例名>.yaml` 中绑定主题、durable 和处理函数。
3. 在业务事务中使用白泽的 Service。例如，更新支付单时用同一个事务登记支付成功事件，提交后由 Outbox 投递。收到事件后发货或开通会员权益，分别由商城和会员系统处理。

## 许可证

[Apache License 2.0](LICENSE)
