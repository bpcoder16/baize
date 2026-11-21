// Package sqliteboot 按实例名装配 SQLite 文件库或内存库。
// 将 sqliteboot.Instances("default") 加入 bootstrap.Plan.Resources，
// 在入口配置同目录提供 sqlite.default.yaml，业务通过 sqlitex.Named("default").DB(ctx) 访问。
package sqliteboot

import (
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/sqlitex"
)

// Instances 声明由白泽在启动时创建、退出时统一关闭的 SQLite 实例。
func Instances(names ...string) bootstrap.Resource {
	return bootstrap.NamedInstances(constant.ResourceSQLite, names, open, sqlitex.CloseAll)
}

type fileConfig struct {
	DSN            string                  `mapstructure:"dsn"`
	JournalMode    sqlitex.JournalMode     `mapstructure:"journalMode"`
	BusyTimeout    time.Duration           `mapstructure:"busyTimeout"`
	Synchronous    sqlitex.SynchronousMode `mapstructure:"synchronous"`
	ForeignKeys    *bool                   `mapstructure:"foreignKeys"`
	Pool           sqlitex.Pool            `mapstructure:"pool"`
	SlowThreshold  time.Duration           `mapstructure:"slowThreshold"`
	LogSQL         *bool                   `mapstructure:"logSQL"`
	InterpolateSQL bool                    `mapstructure:"interpolateSQL"`
}

func open(name string, cfg *fileConfig) error {
	_, err := sqlitex.NewNamed(sqlitex.Config{
		Name:           name,
		DSN:            cfg.DSN,
		JournalMode:    cfg.JournalMode,
		BusyTimeout:    cfg.BusyTimeout,
		Synchronous:    cfg.Synchronous,
		ForeignKeys:    cfg.ForeignKeys,
		Pool:           cfg.Pool,
		SlowThreshold:  cfg.SlowThreshold,
		LogSQL:         cfg.LogSQL == nil || *cfg.LogSQL,
		InterpolateSQL: cfg.InterpolateSQL,
	})
	return err
}
