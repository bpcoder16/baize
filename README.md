<p align="center">
  <img src="assets/baize-logo.png" width="160" alt="白泽 Baize 吉祥物，月白身、玉青鬃、额生双角">
</p>

<h1 align="center">白泽 · Baize</h1>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.27.3%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.27.3+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="许可证 Apache-2.0"></a>
</p>

Baize（bái zé）是基于 Pixiu 的 Go 业务组件库，用来承载多个应用可以共同维护的业务流程。宿主项目保留自己的产品规则。[Pixiu 项目](https://github.com/bpcoder16/pixiu)

项目以传说中能言、通晓万物的神兽白泽命名。吉祥物月白身、玉青鬃，与 Pixiu、Qilin 同为昂首坐姿。

## 组件边界

以支付回调为例，支付组件可以负责渠道验签、重复回调处理、支付单状态更新，并通过 Pixiu 的 Outbox 在更新支付单的同一事务中登记支付成功事件。商城消费事件后发货，会员系统消费事件后开通权益。这两个动作由各自的应用实现。

## 目录与接入

- 每项业务能力对应一个顶层 Go 包。组件的公开入口放在包根目录，存储、HTTP、Cron 或 NATS 处理器按实际需要放在组件自己的 `internal/` 下。
- 宿主在启动时显式装配需要的组件，传入数据库等依赖，把组件提供的路由或处理函数登记到已有服务。数据库连接、调度器和消息客户端的生命周期由宿主管理。
- 部署配置随宿主维护。组件接收完成解析的配置值，可随包提供示例。使用方按业务子包导入，单个组件可以独立接入。

## 当前状态

当前为项目骨架。支付与账号是候选方向，首个业务组件落地时再引入实际使用的 Pixiu 包。

## 许可证

[Apache License 2.0](LICENSE)
