package outbox

import (
	"time"

	"github.com/bpcoder16/baize/constant"
	"gorm.io/gorm"
)

// StoreConfig 借用已有主库，不管理连接生命周期。
type StoreConfig struct {
	Backend constant.Backend
	// MasterDB 是可复用的主库句柄，仓储为每次操作绑定当前 context。
	MasterDB *gorm.DB
	// Location 为 nil 时使用 UTC，决定补偿清理的当地时段及查询结果的时间表示。
	// MySQL 驱动解析时区须与数据库会话时区一致，本配置不修改连接。
	Location *time.Location
}
