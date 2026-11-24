// Package migration 为 MySQL 和 PostgreSQL 的 Prepare 步骤装配表迁移及种子。
package migration

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"github.com/bpcoder16/pixiu/lifecycle"
	"gorm.io/gorm"
)

// SeedOptions 声明宿主提供的 SQL 种子，零值跳过种子阶段。
type SeedOptions struct {
	Files fs.FS                // 提供可信 SQL，通常使用 embed.FS。
	Seeds []schemamigrate.Seed // 按清单顺序校准。
}

type Config struct {
	Enabled bool          `mapstructure:"enabled"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// LoadConfig 读取命名实例的迁移策略，超时默认五分钟。
func LoadConfig(dir, kind, name string) (Config, error) {
	if !bootstrap.ValidNamePart(kind) {
		return Config{}, fmt.Errorf("invalid migration kind %q", kind)
	}
	if !bootstrap.ValidNamePart(name) {
		return Config{}, fmt.Errorf("invalid %s migration instance name %q", kind, name)
	}
	path := filepath.Join(dir, kind+"-migrate."+name+".yaml")
	cfg, err := configx.Parse[Config](path)
	if err != nil {
		return Config{}, fmt.Errorf("load %q: %w", path, err)
	}
	if cfg.Timeout < 0 {
		return Config{}, fmt.Errorf("migration timeout in %q must be non-negative", path)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	return *cfg, nil
}

type masterClient interface {
	MasterDB(context.Context) *gorm.DB
}

// Prepare 按数据库类型和实例名读取开关，执行迁移时借用本次 Plan.Resources 声明的主库。
func Prepare[T masterClient](kind, name string, tables []schemamigrate.Table, seeds SeedOptions, named func(string) T) bootstrap.Step {
	return bootstrap.Step{
		Name: "baize." + kind + "-" + name + "-migrate",
		Init: func(ctx context.Context, dir string, _ *lifecycle.Stack) error {
			cfg, err := LoadConfig(dir, kind, name)
			if err != nil {
				return err
			}
			if !cfg.Enabled || (len(tables) == 0 && len(seeds.Seeds) == 0) {
				return nil
			}
			// 两个阶段共用实例的截止时间，种子只使用迁表后剩余的时间。
			ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			defer cancel()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			client, err := bootstrap.LookupDeclared(ctx, kind, name, named)
			if err != nil {
				return err
			}

			if len(tables) > 0 {
				if _, migrateErr := schemamigrate.Apply(ctx, client.MasterDB, tables); migrateErr != nil {
					return fmt.Errorf("migrate tables: %w", migrateErr)
				}
			}
			if len(seeds.Seeds) == 0 {
				return nil
			}

			return schemamigrate.ApplySeeds(ctx, client.MasterDB, seeds.Files, seeds.Seeds)
		},
	}
}
