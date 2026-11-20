package appconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/appconfig"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIndependentConfigs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "settings.yaml")
	writeConfig(t, file, "env:\n  appName: cards\n  timeLocation: Unknown/Zone\nlog:\n  dir: ./logs\n  names: [http, nats]\n  hookStd: false\n  rotate:\n    every: 24h\ndisabledServices: [cron, nats-consumer:events_1-a, http, websocket]\n")
	if err := os.Symlink(file, "app.yaml"); err != nil {
		t.Fatal(err)
	}
	before := time.Local
	first, err := appconfig.Load("app.yaml")
	if err != nil {
		t.Fatal(err)
	}
	second, err := appconfig.Load("app.yaml")
	if err != nil {
		t.Fatal(err)
	}
	first.Log.Names[0] = "changed"
	*first.Log.HookStd = true
	first.DisabledServices[0] = "changed"
	if second.Env.ConfigDirPath != root || second.Env.TimeLocation != "Unknown/Zone" || second.Env.AppName != "cards" ||
		second.Log.Dir != "./logs" || !slices.Equal(second.Log.Names, []string{"http", "nats"}) ||
		*second.Log.HookStd || second.Log.Rotate.Every != 24*time.Hour || second.DisabledServices[0] != "cron" {
		t.Fatalf("second Load returned %+v", second)
	}
	if time.Local != before {
		t.Fatal("Load changed time.Local")
	}
	if _, err := os.Stat("logs"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("log dir after Load: %v", err)
	}
}

func TestLoadFormats(t *testing.T) {
	formats := map[string]string{
		"yaml": "env:\n  appName: cards",
		"toml": "[env]\nappName = 'cards'",
		"json": `{"env":{"appName":"cards"}}`,
	}
	for ext, body := range formats {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app."+ext)
			writeConfig(t, path, body)
			cfg, err := appconfig.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Env.AppName != "cards" || cfg.Log.HookStd != nil || cfg.Log.Rotate.MaxFiles != 0 || cfg.ResourceCloseTimeout != 0 || cfg.ServiceShutdownTimeout != 0 {
				t.Fatalf("unexpected config %+v", cfg)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	for _, tc := range []struct{ body, field string }{
		{"log:\n  caller: 'false'", "caller"},
		{"env:\n  configDirPath: /wrong", "configdirpath"},
		{"extra: null", "extra"},
		{"serviceShutdownTimeout: -1s", "serviceShutdownTimeout"},
		{"serviceShutdownTimeout: forever", "serviceShutdownTimeout"},
		{"resourceCloseTimeout: -1s", "resourceCloseTimeout"},
		{"resourceCloseTimeout: forever", "resourceCloseTimeout"},
		{"disabledServices: [crno]", "disabledServices[0]"},
		{"disabledServices: [mysql]", "unknown service type"},
		{"disabledServices: [http, 'websocket:']", "disabledServices[1]"},
		{"disabledServices: ['nats-consumer:a:b']", "disabledServices[0]"},
		{"disabledServices: ['http:a*b']", "disabledServices[0]"},
	} {
		p := filepath.Join(t.TempDir(), "app.yaml")
		writeConfig(t, p, tc.body)
		cfg, err := appconfig.Load(p)
		if cfg != nil || err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.field)) {
			t.Errorf("%q: cfg=%v err=%v", tc.body, cfg, err)
		}
	}
	p := filepath.Join(t.TempDir(), "absent.yaml")
	if _, err := appconfig.Load(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := appconfig.Load(""); err == nil {
		t.Fatal("accepted empty path")
	}
}
