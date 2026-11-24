package sqliteboot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpcoder16/pixiu/infra/sqlitex"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestNamedDatabases(t *testing.T) {
	// Pixiu 的命名注册表关闭后不能重开，每组启动与回收在独立进程验证。
	mode := os.Getenv("BAIZE_SQLITE_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "second-fails"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNamedDatabases$")
				cmd.Env = append(os.Environ(), "BAIZE_SQLITE_BOOT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := t.TempDir()
	memory, err := os.ReadFile(filepath.Join("..", "..", "conf.example", "sqlite.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	file := "dsn: 'file:" + filepath.Join(dir, "local.db") + "?_fk=1'\nforeignKeys: false\n"
	if mode == "second-fails" {
		file = "dsn: ':memory:'\njournalMode: WAL\n"
	}
	for name, data := range map[string][]byte{"memory": memory, "file": []byte(file)} {
		if writeErr := os.WriteFile(filepath.Join(dir, "sqlite."+name+".yaml"), data, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	var resources lifecycle.Stack
	defer resources.Close()
	err = Instances("memory", "file").Init(dir, &resources)
	if mode == "second-fails" {
		if err == nil || !strings.Contains(err.Error(), "sqlite.file.yaml") {
			t.Fatalf("应报告失败实例的文件名，实际为 %v", err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		db := sqlitex.Named("file").DB(t.Context())
		var fk int
		if queryErr := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; queryErr != nil || fk != 0 {
			t.Fatalf("显式关闭外键应覆盖 DSN，实际为 %d, %v", fk, queryErr)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "local.db")); statErr != nil {
			t.Fatal(statErr)
		}
	}
	db := sqlitex.Named("memory").DB(t.Context())
	var value int
	if queryErr := db.Raw("SELECT 1").Scan(&value).Error; queryErr != nil || value != 1 {
		t.Fatalf("内存实例查询失败: %v", queryErr)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := resources.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if pingErr := pool.PingContext(t.Context()); pingErr == nil {
		t.Fatal("资源栈未关闭已创建的连接池")
	}
}
