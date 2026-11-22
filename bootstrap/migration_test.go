package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/mysqlboot"
	"github.com/bpcoder16/baize/bootstrap/postgresqlboot"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
)

func TestSameNameMigrationSteps(t *testing.T) {
	tables := []schemamigrate.Table{{}}
	mysql := mysqlboot.PrepareMigration("default", tables, mysqlboot.SeedOptions{})
	pg := postgresqlboot.PrepareMigration("default", tables, postgresqlboot.SeedOptions{})
	if mysql.Name != "baize.mysql-default-migrate" || pg.Name != "baize.postgresql-default-migrate" {
		t.Fatalf("迁移步骤名错误：MySQL %q，PostgreSQL %q", mysql.Name, pg.Name)
	}
	dir := t.TempDir()
	for _, kind := range []string{"mysql", "postgresql"} {
		if err := os.WriteFile(filepath.Join(dir, kind+"-migrate.default.yaml"), []byte("enabled: false"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind              string
		enabled, disabled bootstrap.Step
	}{
		{"mysql", mysql, pg},
		{"postgresql", pg, mysql},
	} {
		file := filepath.Join(dir, tc.kind+"-migrate.default.yaml")
		if err := os.WriteFile(file, []byte("enabled: true"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := tc.disabled.Init(t.Context(), dir, nil); err != nil {
			t.Fatalf("启用 %s 不应开启另一种数据库的迁移: %v", tc.kind, err)
		}
		if err := tc.enabled.Init(t.Context(), dir, nil); err == nil || !strings.Contains(err.Error(), "default") || !strings.Contains(err.Error(), tc.kind) {
			t.Fatalf("已开启的迁移应报告缺少的实例和数据库类型: %v", err)
		}
		if err := os.WriteFile(file, []byte("enabled: false"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
