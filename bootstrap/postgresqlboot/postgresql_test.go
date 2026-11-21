package postgresqlboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestInstancesValidateBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"orders": "{}", "audit": "isDefault: true"} {
		if err := os.WriteFile(filepath.Join(dir, "postgresql."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stack lifecycle.Stack
	defer stack.Close()
	err := Instances("orders", "audit").Init(dir, &stack)
	if err == nil || !strings.Contains(err.Error(), "postgresql.audit.yaml") || !strings.Contains(strings.ToLower(err.Error()), "isdefault") {
		t.Fatalf("应在建连前指出第二个文件中的未知字段: %v", err)
	}
}

func TestFileConfig(t *testing.T) {
	cfg, err := configx.Parse[fileConfig](filepath.Join("..", "..", "conf.example", "postgresql.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Slaves = []addressConfig{{Host: "replica", Port: 5433}}
	cfg.SSLRootCert, cfg.SSLCert, cfg.SSLKey = "ca.pem", "client.pem", "client.key"
	cfg.DisableStatementCache = true
	cfg.LogSQL, cfg.InterpolateSQL = nil, nil
	got := cfg.clientConfig("orders")
	m := got.Master
	if got.Name != "orders" || !got.LogSQL || !got.InterpolateSQL || !got.DisableStatementCache ||
		got.SessionTimeZone != "UTC" || got.InitTimeout != 10*time.Second || m.Host != "127.0.0.1" ||
		m.SSLMode != "verify-full" || m.SSLRootCert != "ca.pem" || m.SSLCert != "client.pem" ||
		m.SSLKey != "client.key" || m.Pool.MaxOpenConns != 100 || len(got.Slaves) != 1 {
		t.Fatal("实例、日志、TLS 或连接池配置不符")
	}
	m.Host, m.Port = "replica", 5433
	if got.Slaves[0] != m {
		t.Fatal("从库应只覆盖连接地址")
	}
	cfg.LogSQL, cfg.InterpolateSQL = new(false), new(false)
	got = cfg.clientConfig("orders")
	if got.LogSQL || got.InterpolateSQL {
		t.Fatal("应保留显式关闭的日志开关")
	}
}
