package constant

// OutboxStatus 表示 Outbox 任务的投递状态。
type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "pending"
	OutboxSucceeded OutboxStatus = "succeeded"
	OutboxFailed    OutboxStatus = "failed"
)

// OutboxHandlerType 是任务持久化的发布方式。
type OutboxHandlerType string

const NatsJetStream OutboxHandlerType = "nats_jetstream"

// Valid 判断类型能否作为两种数据库通用的 handler_type 键。
func (t OutboxHandlerType) Valid() bool {
	// MySQL 的 ascii_bin 忽略尾随空格，PostgreSQL 文本也无法保存零字节。
	if len(t) == 0 || len(t) > 32 || t[len(t)-1] == ' ' {
		return false
	}
	for i := range len(t) {
		if t[i] == 0 || t[i] > 127 {
			return false
		}
	}
	return true
}
