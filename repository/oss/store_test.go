package oss_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	mysqloss "github.com/bpcoder16/baize/model/entity/mysql/oss"
	pgoss "github.com/bpcoder16/baize/model/entity/postgresql/oss"
	model "github.com/bpcoder16/baize/model/oss"
	repo "github.com/bpcoder16/baize/repository/oss"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestStoreUsesEntityColumns(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			db := testdb.Open(t, backend)
			db.NowFunc = func() time.Time { return time.Unix(0, 0) }
			db.NamingStrategy = schema.NamingStrategy{NoLowerCase: true}
			cfg := repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}}
			ctx := t.Context()
			store, err := repo.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasIndex("baize_oss_upload_records", "idx_baize_oss_upload_records_object_key") {
				t.Fatal("缺少 object_key 的命名唯一索引")
			}
			if backend == constant.MySQL {
				pool, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				// 让此测试库的连接返回固定数据库时间，以区分两个时钟。
				pool.SetMaxOpenConns(1)
				if err := db.Exec("SET SESSION timestamp = 946684800").Error; err != nil {
					t.Fatal(err)
				}
			}
			rows := []model.Record{
				{ObjectKey: "files/one", BucketType: "public", WatermarkObjectKey: "wm/one"},
				{ObjectKey: "files/two", BucketType: "private"},
				{ObjectKey: "files/direct", BucketType: "public", Status: model.Success},
			}
			if err := store.Create(ctx, rows); err != nil {
				t.Fatal(err)
			}
			if rows[0].ID == 0 || rows[1].ID == 0 || rows[0].ID == rows[1].ID ||
				rows[0].CreatedAt.IsZero() || rows[0].Status != model.Pending {
				t.Fatalf("批量新增未正确回填 %+v", rows)
			}
			if time.Since(rows[0].CreatedAt).Abs() > 10*time.Second || rows[0].ConfirmedAt != nil ||
				!rows[0].CreatedAt.Equal(rows[0].UpdatedAt) || !rows[0].CreatedAt.Equal(rows[1].CreatedAt) {
				t.Fatalf("待确认记录应使用同一个应用服务器时间: %+v", rows[:2])
			}
			if rows[2].ConfirmedAt == nil || !rows[2].ConfirmedAt.Equal(rows[2].CreatedAt) || !rows[2].ConfirmedAt.Equal(rows[2].UpdatedAt) {
				t.Fatalf("直传记录的三个时间应一致: %+v", rows[2])
			}
			store, err = repo.OpenExisting(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Confirm(ctx, rows[:2]); err != nil {
				t.Fatal(err)
			}
			got, err := store.FindOnMaster(ctx, []uint64{rows[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("按 ID 查询得到 %d 条记录", len(got))
			}
			row := got[0]
			if row.ID != rows[0].ID || row.ObjectKey != "files/one" || row.WatermarkObjectKey != "wm/one" ||
				row.Status != model.Success || row.ConfirmedAt == nil || time.Since(*row.ConfirmedAt).Abs() > 10*time.Second ||
				!row.ConfirmedAt.Equal(row.UpdatedAt) || !row.CreatedAt.Equal(rows[0].CreatedAt) {
				t.Fatalf("读取已确认记录得到 %+v", got)
			}
		})
	}
}

func TestConfirmOrdersUpdatesAndRollsBack(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			db := testdb.Open(t, backend)
			store, err := repo.Open(ctx, repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}})
			if err != nil {
				t.Fatal(err)
			}
			rows := []model.Record{
				{ID: 1, ObjectKey: "one", Status: model.Pending},
				{ID: 2, ObjectKey: "two", Status: model.Success},
			}
			if err := store.Create(ctx, rows); err != nil {
				t.Fatal(err)
			}
			// 先提交高 ID 时会立即冲突；按 ID 加锁则先更新低 ID，再随整批回滚。
			var affected int64
			if err := db.Callback().Update().After("gorm:update").Register("test:count_updates", func(tx *gorm.DB) {
				affected += tx.RowsAffected
			}); err != nil {
				t.Fatal(err)
			}
			reversed := []model.Record{rows[1], rows[0]}
			if err := store.Confirm(ctx, reversed); !errors.Is(err, repo.ErrStateChanged) {
				t.Fatalf("确认冲突得到 %v", err)
			}
			if affected != 1 || reversed[0].ID != 2 {
				t.Fatalf("应按 ID 顺序更新且保留调用方顺序，已更新 %d 行，首个入参 ID 为 %d", affected, reversed[0].ID)
			}
			got, err := store.FindOnMaster(ctx, []uint64{1})
			if err != nil || len(got) != 1 || got[0].Status != model.Pending || got[0].ConfirmedAt != nil {
				t.Fatalf("低 ID 记录应已回滚，得到 %+v，错误 %v", got, err)
			}
		})
	}
}

func TestEntityHasCreateOnlyFields(t *testing.T) {
	for _, entity := range []any{&mysqloss.Record{}, &pgoss.Record{}} {
		parsed, err := schema.Parse(entity, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ID", "Scene", "BucketType", "ObjectKey", "FileExt", "FileSize", "CreatedAt"} {
			field := parsed.LookUpField(name)
			if !field.Creatable || !field.Readable || field.Updatable {
				t.Errorf("%T.%s 应允许创建和读取，禁止通过实体更新", entity, name)
			}
		}
		if !parsed.LookUpField("WatermarkObjectKey").Updatable {
			t.Errorf("%T.WatermarkObjectKey 应允许更新", entity)
		}
	}
}
