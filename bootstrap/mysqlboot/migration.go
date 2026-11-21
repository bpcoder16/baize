package mysqlboot

import (
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/internal/migration"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/mysqlx"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
)

// SeedOptions 声明内嵌种子 SQL 和执行清单。零值仅迁移表结构。
type SeedOptions = migration.SeedOptions

// PrepareMigration 在 Prepare 阶段借用命名 MySQL 主库，先迁移登记的表，再执行种子。
// 声明此步骤时需提供 mysql-migrate.<实例名>.yaml，默认关闭。
func PrepareMigration(name string, tables []schemamigrate.Table, seeds SeedOptions) bootstrap.Step {
	return migration.Prepare(constant.ResourceMySQL, name, tables, seeds, mysqlx.Named)
}
