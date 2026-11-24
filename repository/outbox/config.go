package outbox

import (
	"context"
	"time"

	"github.com/bpcoder16/baize/constant"
	"gorm.io/gorm"
)

// DBClient 提供绑定了调用方 context 的主、从库会话。
// 未配置从库时由客户端回退主库。
type DBClient interface {
	MasterDB(context.Context) *gorm.DB
	SlaveDB(context.Context) *gorm.DB
}

// StoreConfig 选择发件箱所在的数据库，连接池由宿主管理。
type StoreConfig struct {
	Backend constant.Backend
	Client  DBClient
	// Location 为 nil 时使用 UTC，决定补偿清理的当地时段及查询结果的时间表示。
	// MySQL 驱动解析时区须与数据库会话时区一致，本配置不修改连接。
	Location *time.Location
}
