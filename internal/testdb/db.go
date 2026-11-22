// Package testdb 为数据库集成测试提供独立的临时数据库。测试结束时只回收本次创建的库。
package testdb

import (
	"database/sql"
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

// Database 保存已指向临时库的驱动配置，测试可以打开连接或生成宿主配置。
type Database struct {
	MySQL      *driver.Config
	PostgreSQL *pgx.ConnConfig
}

// Create 只创建随机命名的隔离库。测试结束时先回收后登记的业务连接，再删除该库。
func Create(t *testing.T, backend constant.Backend) Database {
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
	create := "CREATE DATABASE " + name
	var admin *sql.DB
	var db Database
	switch backend {
	case constant.MySQL:
		cfg, err := driver.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = ""
		admin, err = sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		create += " CHARACTER SET utf8mb4"
		cfg.DBName = name
		db.MySQL = cfg
	case constant.PostgreSQL:
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Database = "postgres"
		admin = stdlib.OpenDB(*cfg)
		cfg.Database = name
		db.PostgreSQL = cfg
	default:
		t.Fatalf("不支持的测试数据库 %q", backend)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if _, err := admin.ExecContext(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE " + name); err != nil {
			t.Error(err)
		}
	})
	return db
}

// Open 在独立临时库上打开 GORM 连接，可为 MySQL 指定会话和驱动共用的时区。
func Open(t *testing.T, backend constant.Backend, mysqlLocation ...*time.Location) *gorm.DB {
	t.Helper()
	db := Create(t, backend)
	var pool *sql.DB
	var dialect gorm.Dialector
	if cfg := db.MySQL; cfg != nil {
		var err error
		cfg.ParseTime = true
		if len(mysqlLocation) > 0 {
			cfg.Loc = mysqlLocation[0]
		} else {
			cfg.Loc, err = time.LoadLocation("Asia/Shanghai")
			if err != nil {
				t.Fatal(err)
			}
		}
		if cfg.Params == nil {
			cfg.Params = make(map[string]string)
		}
		// TIMESTAMP 由会话转换，驱动的解析时区必须与每条连接保持一致。
		cfg.Params["time_zone"] = "'" + time.Now().In(cfg.Loc).Format("-07:00") + "'"
		pool, err = sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		dialect = mysql.New(mysql.Config{Conn: pool})
	} else {
		pool = stdlib.OpenDB(*db.PostgreSQL)
		dialect = postgres.New(postgres.Config{Conn: pool})
	}
	t.Cleanup(func() { _ = pool.Close() })
	conn, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
