// Package outbox 定义仓储与发布服务共用的任务快照。
package outbox

import (
	"time"

	"github.com/bpcoder16/baize/constant"
)

// Task 是业务事务内登记的不可变任务。
type Task struct {
	HandlerType  constant.OutboxHandlerType
	ResourceName string
	Payload      []byte
	ExpiresAt    time.Time
}

// Delivery 是交给发布器的一次投递，ID 在补偿时保持不变。
type Delivery struct {
	ID           int64
	ResourceName string
	Payload      []byte
}

// JetStreamPayload 定义消息正文。目标和截止时间使用独立的表字段。
type JetStreamPayload struct {
	Subject string              `json:"subject"`
	Data    []byte              `json:"data"`
	Header  map[string][]string `json:"header"`
}
