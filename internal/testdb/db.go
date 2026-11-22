// Package testdb 为数据库集成测试提供独立的临时数据库。测试结束时只回收本次创建的库。
package testdb

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	driver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 只在随机命名的隔离数据库中迁移和写入，不修改 DSN 原有数据库。
func Open(t *testing.T, backend constant.Backend, mysqlLocation ...*time.Location) *gorm.DB {
	t.Helper()
	key := "BAIZE_OUTBOX_MYSQL_DSN"
	if backend == constant.PostgreSQL {
		key = "BAIZE_OUTBOX_POSTGRESQL_DSN"
	}
	dsn := os.Getenv(key)
	if dsn == "" {
		t.Skip("未设置 " + key + "；真实数据库验收未运行")
	}
	name := fmt.Sprintf("baize_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	gormConfig := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var admin, db *gorm.DB
	var err error
	created := false
	if backend == constant.MySQL {
		cfg, parseErr := driver.ParseDSN(dsn)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		cfg.DBName = ""
		cfg.ParseTime = true
		cfg.Loc, err = time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		if len(mysqlLocation) > 0 {
			cfg.Loc = mysqlLocation[0]
		}
		if cfg.Params == nil {
			cfg.Params = make(map[string]string)
		}
		// TIMESTAMP 由会话转换，驱动的解析时区必须与每条连接保持一致。
		cfg.Params["time_zone"] = "'" + time.Now().In(cfg.Loc).Format("-07:00") + "'"
		admin, err = gorm.Open(mysql.Open(cfg.FormatDSN()), gormConfig)
		if err == nil {
			err = admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4").Error
			created = err == nil
		}
		if err == nil {
			cfg.DBName = name
			db, err = gorm.Open(mysql.Open(cfg.FormatDSN()), gormConfig)
		}
	} else {
		cfg, parseErr := pgx.ParseConfig(dsn)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		cfg.Database = "postgres"
		pool := stdlib.OpenDB(*cfg)
		admin, err = gorm.Open(postgres.New(postgres.Config{Conn: pool}), gormConfig)
		if err == nil {
			err = admin.Exec("CREATE DATABASE " + name).Error
			created = err == nil
		}
		if err == nil {
			cfg.Database = name
			pool = stdlib.OpenDB(*cfg)
			db, err = gorm.Open(postgres.New(postgres.Config{Conn: pool}), gormConfig)
		}
	}
	if admin != nil && admin.Statement != nil {
		t.Cleanup(func() {
			if db != nil {
				pool, _ := db.DB()
				_ = pool.Close()
			}
			if created {
				if err := admin.Exec("DROP DATABASE IF EXISTS " + name).Error; err != nil {
					t.Error(err)
				}
			}
			pool, _ := admin.DB()
			_ = pool.Close()
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return db
}
