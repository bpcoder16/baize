package elasticsearchboot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bpcoder16/pixiu/infra/configx"
)

func TestFileConfig(t *testing.T) {
	dir := filepath.Join("..", "..", "conf.example")
	cfg, err := configx.Parse[fileConfig](filepath.Join(dir, "elasticsearch.default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.clientConfig(dir, "search")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "search" || len(got.Addresses) != 1 ||
		got.StartupTimeout != 5*time.Second || got.DialTimeout != 5*time.Second ||
		got.MaxIdleConns != 100 || got.MaxIdleConnsPerHost != 10 || got.IdleConnTimeout != 90*time.Second ||
		got.SlowThreshold != 200*time.Millisecond {
		t.Fatal("模板中的实例、连接池或超时配置未正确传入客户端")
	}
	dir = t.TempDir()
	cfg.CACertFile = "ca.pem"
	if _, clientErr := cfg.clientConfig(dir, "search"); !errors.Is(clientErr, os.ErrNotExist) {
		t.Fatalf("应保留 CA 文件读取错误，实际为 %v", clientErr)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, cfg.CACertFile), nil, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, clientErr := cfg.clientConfig(dir, "search"); clientErr == nil {
		t.Fatal("指定空 CA 文件时应报错")
	}
}
