package outbox_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	mysqloutbox "github.com/bpcoder16/baize/model/entity/mysql/outbox"
	pgoutbox "github.com/bpcoder16/baize/model/entity/postgresql/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 无数据库时仍检查生成的 DDL 与模型更新语句。
func TestRecordModel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dialector gorm.Dialector
		model     any
		columns   []string
	}{
		{
			name:      "mysql",
			model:     &mysqloutbox.Record{},
			dialector: mysql.New(mysql.Config{SkipInitializeWithVersion: true}),
			columns: []string{
				"`handler_type` varchar(32) character set ascii collate ascii_bin not null",
				"`resource_name` varchar(64) character set ascii collate ascii_bin not null",
				"`status` varchar(16) character set ascii collate ascii_bin",
				"`created_at` timestamp(6) not null default current_timestamp(6)",
				"`updated_at` timestamp(6) not null default current_timestamp(6)",
				"`expires_at` timestamp(6) null default null",
				"`finished_at` timestamp(6) null default null",
			},
		},
		{
			name:      "postgresql",
			model:     &pgoutbox.Record{},
			dialector: postgres.New(postgres.Config{}),
			columns: []string{
				`"handler_type" varchar(32) collate "c" not null`,
				`"resource_name" varchar(64) collate "c" not null`,
				`"status" varchar(16) collate "c"`,
				`"created_at" timestamptz not null default current_timestamp,`,
				`"updated_at" timestamptz not null default current_timestamp,`,
				`"expires_at" timestamptz,`,
				`"finished_at" timestamptz,`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			db, err := gorm.Open(tc.dialector, &gorm.Config{
				DisableAutomaticPing:   true,
				DryRun:                 true,
				SkipDefaultTransaction: true,
				Logger: logger.New(log.New(&output, "", 0), logger.Config{
					LogLevel: logger.Info,
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = pool.Close() })
			if err := db.Migrator().CreateTable(tc.model); err != nil {
				t.Fatal(err)
			}
			ddl := strings.ToLower(output.String())
			for _, column := range tc.columns {
				if !strings.Contains(ddl, column) {
					t.Errorf("迁移缺少列定义 %q: %s", column, ddl)
				}
			}
			if strings.Contains(ddl, "on update") {
				t.Fatalf("时间更新必须由 Store 控制，不能隐式 ON UPDATE: %s", ddl)
			}
			now := time.Now()
			values := map[string]any{
				"id":            2,
				"resource_name": "other",
				"handler_type":  "other",
				"payload":       []byte("other"),
				"expires_at":    now,
				"log_id":        "other",
				"created_at":    now,
				"attempts":      1,
			}
			insert := db.Model(tc.model).Create(values)
			if insert.Error != nil {
				t.Fatal(insert.Error)
			}
			columns, _, _ := strings.Cut(insert.Statement.SQL.String(), " VALUES ")
			for _, name := range []string{"expires_at", "log_id", "created_at"} {
				if !strings.Contains(columns, insert.Statement.Quote(name)) {
					t.Errorf("创建时缺少字段 %s: %s", name, columns)
				}
			}
			tx := db.Model(tc.model).Where("id = ?", 1).Updates(values)
			if tx.Error != nil {
				t.Fatal(tx.Error)
			}
			stmt := tx.Statement.SQL.String()
			if len(tx.Statement.Vars) != 2 || !strings.Contains(stmt, " SET "+tx.Statement.Quote("attempts")+"=") {
				t.Fatalf("更新只应写入 attempts: %s", stmt)
			}
		})
	}
}

func TestMySQLTimestampDefaultsAndRepeatedMigration(t *testing.T) {
	const sqlMode = "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"
	dsn := os.Getenv("BAIZE_OUTBOX_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 BAIZE_OUTBOX_MYSQL_DSN；真实数据库验收未运行")
	}
	for _, mode := range []string{"0", "1"} {
		t.Run("explicit_defaults_for_timestamp="+mode, func(t *testing.T) {
			cfg, err := driver.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Params == nil {
				cfg.Params = make(map[string]string)
			}
			// 仅临时测试连接使用这组参数；liveStore 创建隔离库，不修改实例或业务连接。
			cfg.Params["explicit_defaults_for_timestamp"] = mode
			cfg.Params["sql_mode"] = "'" + sqlMode + "'"
			cfg.Params["timestamp"] = "946684800"
			t.Setenv("BAIZE_OUTBOX_MYSQL_DSN", cfg.FormatDSN())
			store, db := liveStore(t, constant.MySQL, time.UTC)
			ctx := context.Background()
			id := enqueue(t, store, db)
			record, err := store.TestReadRecord(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if record.ExpiresAt != nil || record.FinishedAt != nil {
				t.Fatalf("未设置的期限和完成时间必须为 NULL: %+v", record)
			}
			if time.Since(record.CreatedAt).Abs() > 10*time.Second || !record.CreatedAt.Equal(record.UpdatedAt) {
				t.Fatalf("创建和更新时间应使用同一个应用服务器时间: %+v", record)
			}

			// 同时尝试改写两个时间列，只有更新时间应当采用过去的基准值。
			createdAt := record.CreatedAt
			baseline := createdAt.Add(-time.Hour)
			if err := db.Model(&mysqloutbox.Record{}).Where("id = ?", id).Updates(map[string]any{
				"created_at": baseline,
				"updated_at": baseline,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&mysqloutbox.Record{}).Where("id = ?", id).Update("last_error", "保留时间列").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := schemamigrate.Apply(ctx, db.WithContext, []schemamigrate.Table{{Model: &mysqloutbox.Record{}}}); err != nil {
				t.Fatalf("重复迁移失败: %v", err)
			}
			record, err = store.TestReadRecord(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if !record.CreatedAt.Equal(createdAt) || !record.UpdatedAt.Equal(baseline) || record.ExpiresAt != nil || record.FinishedAt != nil {
				t.Fatalf("无关字段更新和重复迁移不得改变时间列: %+v", record)
			}
			worker := testWorker(t, store, func(context.Context, model.Delivery) error {
				return nil
			})
			if err := worker.Send(ctx, id); err != nil {
				t.Fatal(err)
			}
			record, err = store.TestReadRecord(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if record.Status != constant.OutboxSucceeded || !record.CreatedAt.Equal(createdAt) || !record.UpdatedAt.After(baseline) || record.ExpiresAt != nil ||
				record.FinishedAt == nil || !record.FinishedAt.Equal(record.UpdatedAt) || time.Since(record.UpdatedAt).Abs() > 10*time.Second {
				t.Fatalf("发送成功应显式回写更新和完成时间，保持创建时间和空期限: %+v", record)
			}
			var actual struct {
				ExplicitDefaults string
				SQLMode          string
			}
			if err := db.Raw("SELECT @@SESSION.explicit_defaults_for_timestamp AS explicit_defaults, @@SESSION.sql_mode AS sql_mode").Scan(&actual).Error; err != nil {
				t.Fatal(err)
			}
			if actual.ExplicitDefaults != mode || actual.SQLMode != sqlMode {
				t.Fatalf("迁移和发送不得修改会话参数: got %+v, want %s / %s", actual, mode, sqlMode)
			}
		})
	}
}
