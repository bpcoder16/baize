package outbox

import (
	"time"

	"github.com/bpcoder16/baize/constant"
)

// Record 是一次查询返回的任务快照。时间按 Store 配置的时区表示，空期限和完成时间保留为 nil。
type Record struct {
	ID           int64
	HandlerType  constant.OutboxHandlerType
	ResourceName string
	Payload      []byte
	Status       constant.OutboxStatus
	Attempts     int64
	ExpiresAt    *time.Time
	LastError    string
	LogID        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	FinishedAt   *time.Time
}
