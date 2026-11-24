package clickhouseboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestInstancesValidateBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"events": "{}", "archive": "master: {protocol: http}"} {
		if err := os.WriteFile(filepath.Join(dir, "clickhouse."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stack lifecycle.Stack
	defer stack.Close()
	err := Instances("events", "archive").Init(dir, &stack)
	if err == nil || !strings.Contains(err.Error(), "clickhouse.archive.yaml") || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("应在建连前指出从属地址上误填的协议: %v", err)
	}
}

func TestFileConfig(t *testing.T) {
	cfg, parseErr := configx.Parse[fileConfig](filepath.Join("..", "..", "conf.example", "clickhouse.default.yaml"))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	cfg.Slaves = []addressConfig{{Host: "replica", Port: 8123}}
	for _, tt := range []struct {
		name string
		want clickhouse.Protocol
	}{
		{"", clickhouse.Native},
		{"native", clickhouse.Native},
		{"http", clickhouse.HTTP},
	} {
		cfg.Protocol = tt.name
		cfg.TLS = tt.want == clickhouse.HTTP
		got, err := cfg.clientConfig("analytics")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "analytics" || got.Master.Protocol != tt.want || len(got.Slaves) != 1 ||
			got.Master.Pool.MaxOpenConns != 100 || got.InitTimeout != 10*time.Second {
			t.Fatal("协议、主从或连接池配置不符")
		}
		s := got.Slaves[0]
		if s.Host != "replica" || s.Port != 8123 || s.Protocol != tt.want || s.Database != got.Master.Database ||
			s.Settings["session_timezone"] != "UTC" || (s.TLS != nil) != cfg.TLS ||
			(s.TLS != nil && s.TLS.InsecureSkipVerify) {
			t.Fatal("从库的连接参数、会话设置或 TLS 校验有误")
		}
	}
	cfg.Protocol = "htp"
	if _, err := cfg.clientConfig("analytics"); err == nil {
		t.Fatal("应拒绝未知协议")
	}
}
