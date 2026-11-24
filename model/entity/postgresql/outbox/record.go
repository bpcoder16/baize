// Package outbox 定义 PostgreSQL 事务发件箱的表结构。
package outbox

import (
	"time"

	"github.com/bpcoder16/baize/constant"
)

// Record 映射 PostgreSQL 的 baize_outbox 表，发送结果由 Store 在持锁事务内显式更新。
// 时间列使用 PostgreSQL 原生微秒精度，避免 GORM 自动迁移将类型精度误判为列长度。
type Record struct {
	ID           int64                      `gorm:"column:id;type:bigserial;primaryKey;<-:create;index:baize_outbox_ready_idx,priority:2;index:baize_outbox_cleanup_idx,priority:4;comment:数据库自增任务ID，重试保持不变"`
	HandlerType  constant.OutboxHandlerType `gorm:"column:handler_type;type:varchar(32) COLLATE \"C\";size:32;not null;<-:create;index:baize_outbox_cleanup_idx,priority:1;comment:任务发布方式"`
	ResourceName string                     `gorm:"column:resource_name;type:varchar(64) COLLATE \"C\";size:64;not null;<-:create;comment:投递目标资源实例名"`
	Payload      []byte                     `gorm:"column:payload;type:bytea;not null;<-:create;comment:不可变任务内容，最大1MiB"`
	Status       constant.OutboxStatus      `gorm:"column:status;type:varchar(16) COLLATE \"C\";size:16;not null;index:baize_outbox_ready_idx,priority:1;index:baize_outbox_cleanup_idx,priority:2;comment:任务状态，pending待发送或重试、succeeded成功、failed已过期或目标无效"`
	Attempts     int64                      `gorm:"column:attempts;type:bigint;not null;comment:发送尝试次数，包含首次"`
	ExpiresAt    *time.Time                 `gorm:"column:expires_at;type:timestamptz;<-:create;comment:发送截止时间，空表示不过期"`
	LastError    string                     `gorm:"column:last_error;type:text;not null;comment:最近一次失败原因，最大4KiB，成功后清空"`
	LogID        string                     `gorm:"column:log_id;type:text;not null;<-:create;comment:登记时的上下文logId"`
	CreatedAt    time.Time                  `gorm:"column:created_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP;autoCreateTime:false;<-:create;index:baize_outbox_cleanup_idx,priority:3;comment:创建时间，按配置时区读取"`
	UpdatedAt    time.Time                  `gorm:"column:updated_at;type:timestamptz;not null;default:CURRENT_TIMESTAMP;autoUpdateTime:false;comment:更新时间，按配置时区读取"`
	FinishedAt   *time.Time                 `gorm:"column:finished_at;type:timestamptz;comment:任务进入终态的时间"`
}

func (*Record) TableName() string { return "baize_outbox" }
