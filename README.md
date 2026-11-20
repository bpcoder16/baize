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

当前实现应用配置读取、进程环境、时区和日志初始化。Pixiu 版本为 `v0.1.12`。资源装配、业务准备和常驻服务将在后续阶段接入。

## 初始化一个宿主

将 [app.yaml 模板](conf.example/app.yaml) 复制到宿主的 `conf/app.yaml`。最小入口如下。

```go
package main

import (
    "os"

    "github.com/bpcoder16/baize/bootstrap"
)

func main() {
    os.Exit(bootstrap.RunCLI())
}
```

在宿主目录运行 `go run .`，白泽读取配置并初始化环境、时区和日志，写入“白泽初始化完成”后关闭日志并退出。`go run . --conf /etc/cards/staging.yaml` 可指定其他文件，短参数为 `-c`，`go run . -h` 显示帮助。

需要在初始化后继续工作的宿主可调用 `resources, err := bootstrap.Init(path)`，成功后持有资源栈，工作结束时调用 `resources.Close()` 并处理关闭错误。环境在每个进程内只发布一次。配置检查工具可单独调用 `appconfig.Load(path)`。相对路径、兼容字段和初始化边界见[包说明](appconfig/doc.go)。

## 目标目录

目录沿用业务项目的职责分层。以 Outbox 为例，表模型放在 `model/entity/outbox/`，事务登记和状态回写放在 `repository/outbox/`，投递与补偿流程放在 `service/outbox/`，JetStream 发布和 Cron 入口分别放在 `natspublisher/`、`cronhandler/`。宿主可以按需导入各层的公开包。

`bootstrap/` 负责应用装配。需要装配的业务能力在这里提供统一入口，把分散在各层的实现接到宿主已有的表迁移、路由、任务和订阅清单。实际用到哪一层，就在哪一层下沉代码。

## 后续的 Outbox 接入设计

1. 在宿主启动清单中选择共享实例和业务能力，并登记项目自己的处理函数。例如，已有 MySQL 和 NATS 的应用只需增加一项 Outbox 声明，引用已有实例。
2. 在宿主配置中指定部署行为。Outbox 的表随宿主迁移策略准备，补偿任务通过 `cron.yaml` 启用。需要消费事件的应用在自己的 `nats-register.<实例名>.yaml` 中绑定主题、durable 和处理函数。
3. 在业务事务中使用白泽的 Service。例如，更新支付单时用同一个事务登记支付成功事件，提交后由 Outbox 投递。收到事件后发货或开通会员权益，分别由商城和会员系统处理。

## 许可证

[Apache License 2.0](LICENSE)
