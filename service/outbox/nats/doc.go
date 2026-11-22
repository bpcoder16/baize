// Package nats 将 JetStream 发布接入 Outbox，发送成功以有效 PubAck 为准。
//
// outboxboot.PrepareJetStream 装配发布和补偿，业务通过 service/outbox 登记和发送任务。
// NewJetStreamHandler 接收启动时已验证的 NATS 客户端，发送时按记录中的 resource_name 选择客户端。
// 消息主题使用原始 subject，由 natsx 添加 namespace。业务入队时校验 Header 名，发布时由 NATS SDK 规范化 Header 值。
// 发布使用任务 ID 的十进制字符串作为 Nats-Msg-Id，重复确认也算成功。
// 不同数据库可能生成相同的任务 ID，应使用各自的 Stream。
package nats
