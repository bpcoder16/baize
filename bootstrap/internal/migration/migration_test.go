package migration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"github.com/bpcoder16/pixiu/infra/sqlitex"
	"gorm.io/gorm"
)

type testItem struct {
	ID int
}

type testMaster func(context.Context) *gorm.DB

func (f testMaster) MasterDB(ctx context.Context) *gorm.DB { return f(ctx) }

func TestMigrateTablesThenSeeds(t *testing.T) {
	client, err := sqlitex.New(sqlitex.Config{Name: "test", DSN: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mysql-migrate.orders.yaml"), []byte("enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var deadlines []time.Time
	master := testMaster(func(ctx context.Context) *gorm.DB {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("迁移步骤应设置截止时间")
		}
		deadlines = append(deadlines, deadline)
		return client.DB(ctx)
	})
	named := func(string) testMaster { return master }
	seeds := SeedOptions{
		Files: fstest.MapFS{"items.sql": {Data: []byte("INSERT INTO test_items (id) VALUES (1);")}},
		Seeds: []schemamigrate.Seed{{Model: &testItem{}, File: "items.sql"}},
	}
	tables := []schemamigrate.Table{{Model: &testItem{}}}
	start := time.Now()
	step := Prepare("mysql", "orders", tables, seeds, named)
	if err := step.Init(t.Context(), dir, nil); err != nil {
		t.Fatal(err)
	}
	end := time.Now()
	if len(deadlines) != 2 {
		t.Fatalf("期待两个阶段，实际期限为 %v", deadlines)
	}
	if !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("迁表和种子应共用截止时间，实际为 %v", deadlines)
	}
	if deadline := deadlines[0]; deadline.Before(start.Add(5*time.Minute)) || deadline.After(end.Add(5*time.Minute)) {
		t.Fatalf("默认总时限应为 5 分钟，实际截止时间为 %v", deadline)
	}
	var count int64
	if err := client.DB(t.Context()).Model(&testItem{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("迁移后应写入种子，count=%d, err=%v", count, err)
	}
	deadlines = nil
	if err := Prepare("mysql", "orders", []schemamigrate.Table{{}}, seeds, named).Init(t.Context(), dir, nil); err == nil || len(deadlines) != 1 {
		t.Fatalf("结构迁移失败后应跳过种子，期限 %v，错误 %v", deadlines, err)
	}
}

func TestMigrationCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "postgresql-migrate.orders.yaml"), []byte("enabled: true\ntimeout: 30s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"tables", "seeds"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			start := time.Now()
			named := func(string) testMaster {
				return func(stage context.Context) *gorm.DB {
					deadline, ok := stage.Deadline()
					if !ok || deadline.Before(start.Add(30*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {
						t.Fatalf("阶段应使用配置的 30 秒总时限，实际截止时间为 %v", deadline)
					}
					cancel()
					if !errors.Is(stage.Err(), context.Canceled) {
						t.Fatal("阶段 context 未继承 Prepare 取消信号")
					}
					return &gorm.DB{Error: stage.Err()}
				}
			}
			tables := []schemamigrate.Table{{Model: &testItem{}}}
			seeds := SeedOptions{Files: fstest.MapFS{}, Seeds: []schemamigrate.Seed{{Model: &testItem{}}}}
			if phase == "seeds" {
				tables = nil
			}
			err := Prepare("postgresql", "orders", tables, seeds, named).Init(ctx, dir, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("应保留取消错误，实际为 %v", err)
			}
		})
	}
}

func TestMigrationConfig(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body, want string
	}{
		{"missing", "", "mysql-migrate.missing.yaml"},
		{"../other", "", "instance name"},
		{"unknown", "enable: true", "enable"},
		{"negative", "timeout: -1s", "timeout"},
		{"empty", "enabled: true", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(dir, "mysql-migrate."+tc.name+".yaml"), []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			step := Prepare("mysql", tc.name, nil, SeedOptions{}, func(string) testMaster {
				t.Fatal("空清单不应取主库")
				return nil
			})
			err := step.Init(t.Context(), dir, nil)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("配置错误 %v，预期包含 %q", err, tc.want)
			}
		})
	}
}
