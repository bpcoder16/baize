// Package nats 将 JetStream 发布接入 Outbox，发送成功以有效 PubAck 为准。
//
// outboxboot.PrepareJetStream 装配发布和补偿，业务通过 service/outbox 登记和发送任务。
// NewJetStreamHandler 绑定默认名称和对应的 NATS 客户端映射，入队与投递共用这些目标。
// 消息主题使用原始 subject，由 natsx 添加 namespace。业务入队时校验 Header 名，发布时由 NATS SDK 规范化 Header 值。
// 发布使用任务 ID 的十进制字符串作为 Nats-Msg-Id，重复确认也算成功。
// JetStream 在整个 Stream 内去重。共用 Stream 的其他发布方若设置 Nats-Msg-Id，应避开这张表的十进制 ID。
package nats
