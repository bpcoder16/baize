package outbox

import (
	"github.com/bpcoder16/baize/constant"
	mysqloutbox "github.com/bpcoder16/baize/model/entity/mysql/outbox"
	pgoutbox "github.com/bpcoder16/baize/model/entity/postgresql/outbox"
	"gorm.io/gorm/schema"
)

type recordEntity interface {
	mysqloutbox.Record | pgoutbox.Record
}

// 每次返回独立模型，避免 GORM 在并发事务间共享可写的字段值。
func (s *Store) tableModel() schema.Tabler {
	if s.cfg.Backend == constant.MySQL {
		return &mysqloutbox.Record{}
	}
	return &pgoutbox.Record{}
}
