package bootstrap

import (
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/appconfig"
)

func TestPrepareLogConfig(t *testing.T) {
	cfg := appconfig.LogConfig{Format: "json"}
	got, err := prepareLogConfig(cfg, "cards")
	if err != nil || got.Rotate.Every != time.Hour || got.Rotate.MaxFiles != 48 || cfg.Rotate != (appconfig.LogRotateConfig{}) {
		t.Fatalf("default rotation: %+v, %v", got.Rotate, err)
	}
	for _, tc := range []struct {
		cfg   appconfig.LogConfig
		field string
	}{
		{appconfig.LogConfig{}, "log.format"},
		{appconfig.LogConfig{Format: "text", Rotate: appconfig.LogRotateConfig{MaxFiles: 2}}, "log.rotate.maxFiles"},
		{appconfig.LogConfig{Format: "json", Names: []string{"http", "http"}}, "log.names"},
		{appconfig.LogConfig{Format: "json", Names: []string{"a/b"}}, "log.names"},
	} {
		if _, err := prepareLogConfig(tc.cfg, "cards"); err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s: got %v", tc.field, err)
		}
	}
}
