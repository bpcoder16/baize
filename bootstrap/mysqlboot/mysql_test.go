package mysqlboot

import (
	"path/filepath"
	"testing"

	"github.com/bpcoder16/pixiu/infra/configx"
)

func TestFileConfig(t *testing.T) {
	cfg, err := configx.Parse[fileConfig](filepath.Join("..", "..", "conf.example", "mysql.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Slaves = []addressConfig{{Host: "replica", Port: 3307}}
	cfg.LogSQL = nil
	enabled := false
	cfg.InterpolateSQL = &enabled
	got := cfg.clientConfig("orders")
	if got.Name != "orders" || !got.LogSQL || got.InterpolateSQL || len(got.Slaves) != 1 {
		t.Fatalf("实例标识、日志配置或从库数量错误")
	}
	want := got.Master
	want.Host, want.Port = "replica", 3307
	if got.Slaves[0] != want {
		t.Fatal("从库应沿用实例的公共连接参数，仅覆盖地址")
	}
}
