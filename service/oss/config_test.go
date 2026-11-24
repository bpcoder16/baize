package oss

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "github.com/bpcoder16/baize/model/oss"
)

func TestSceneConfiguration(t *testing.T) {
	template, err := os.ReadFile("../../conf.example/baize.oss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "baize.oss.yaml")
	configText := strings.Replace(string(template), "stsSessionName: \"oss-upload\"\n", "", 1)
	configText = strings.Replace(configText, `privateBucketURLExpires: "15m"`, "", 1)
	if err := os.WriteFile(file, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	// 缺省会话名和私有签名有效期由场景配置统一补齐。
	if cfg.STSSessionName != "oss-upload" || *cfg.PrivateBucketURLExpires != 15*time.Minute {
		t.Fatalf("unexpected session defaults: %q", cfg.STSSessionName)
	}
	if size := cfg.MaxDirectUploadSize(); size != 10<<20 {
		t.Fatalf("unexpected upload size limit: %d", size)
	}
	service := Uploads{config: *cfg}
	for _, tc := range []struct {
		scene string
		want  error
	}{
		{"config-file", ErrSize},
		{"CONFIG-FILE", ErrSize},
		{"configFile", ErrScene},
		{"configfile", ErrScene},
	} {
		_, err := service.CreateToken(context.Background(), model.OSSUploadTokenRequest{Scene: tc.scene})
		if !errors.Is(err, tc.want) {
			t.Errorf("scene %q: got %v, want %v", tc.scene, err, tc.want)
		}
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(template), `"15m"`, `"0s"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(file); err == nil || !strings.Contains(err.Error(), "privateBucketURLExpires") {
		t.Fatalf("explicit zero private TTL: %v", err)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`  config-file:`, `  " config-file":`, `name must contain 1 to 64 bytes without leading or trailing whitespace`},
		{`bucketType: "public"`, `bucketType: "unknown"`, `bucketType must be "public" or "private"`},
		{`targetDir: "config-file"`, `targetDir: ""`, `targetDir must contain 1 to 400 bytes`},
		{`targetDir: "config-file"`, `targetDir: ".."`, `targetDir must be a clean relative path without "." or ".." segments`},
		{`targetDir: "config-file"`, `targetDir: "../config"`, `targetDir must be a clean relative path without "." or ".." segments`},
		{`targetDir: "config-file"`, `targetDir: "config/*"`, `targetDir must not contain backslashes, wildcards, or line breaks`},
		{`maxBatchCount: 1`, `maxBatchCount: 0`, `maxBatchCount must be between 1 and 100`},
		{`maxSize: 10485760`, `maxSize: -1`, `maxSize must be between 1 and 1073741824 bytes`},
		{"allowedExts:\n      - \".json\"", `allowedExts: []`, `allowedExts must not be empty`},
		{`".json"`, `".*"`, `allowedExts entry ".*" must be a dot followed by 1 to 19 lowercase letters or digits`},
		{`bucketType:`, `bucktType:`, ""},
	} {
		text := strings.Replace(string(template), tc.from, tc.to, 1)
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if invalid, err := LoadConfig(file); err == nil || invalid != nil {
			t.Errorf("accepted invalid config: %s", tc.to)
		} else if tc.want != "" && (!strings.Contains(err.Error(), "config-file") || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("config %s: got error %q, want scene name and %q", tc.to, err, tc.want)
		}
	}
}

func TestSceneNameByteLimit(t *testing.T) {
	prefix := strings.Repeat("界", 21)
	cfg := Config{Scenes: map[string]Scene{
		prefix + "i": {
			BucketType:    "public",
			TargetDir:     "files",
			MaxBatchCount: 1,
			MaxSize:       1024,
			AllowedExts:   []string{".json"},
		},
	}}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	service := Uploads{config: cfg}
	// İ 转小写后是单字节的 i，长度检查须在场景查找前进行。
	for _, tc := range []struct {
		name      string
		scene     string
		tokenErr  error
		uploadErr error
	}{
		{"64 bytes", prefix + "I", ErrSize, ErrParams},
		{"65 bytes", prefix + "İ", ErrScene, ErrScene},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.CreateToken(t.Context(), model.OSSUploadTokenRequest{Scene: tc.scene})
			if !errors.Is(err, tc.tokenErr) {
				t.Errorf("CreateToken error = %v, want %v", err, tc.tokenErr)
			}
			_, err = service.DirectUpload(t.Context(), tc.scene, nil)
			if !errors.Is(err, tc.uploadErr) {
				t.Errorf("DirectUpload error = %v, want %v", err, tc.uploadErr)
			}
		})
	}
}

func TestSceneSTSDuration(t *testing.T) {
	template, err := os.ReadFile("../../conf.example/baize.oss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Replace(string(template), "    stsDuration: \"15m\"\n", "", 1)
	file := filepath.Join(t.TempDir(), "baize.oss.yaml")
	for _, tc := range []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"omitted", "", 40 * time.Minute, false},
		{"explicit default", `"40m"`, 40 * time.Minute, false},
		{"custom duration", `"90m"`, 90 * time.Minute, false},
		{"minimum duration", `"15m"`, 15 * time.Minute, false},
		{"maximum duration", `"12h"`, 12 * time.Hour, false},
		{"below minimum", `"14m59s"`, 0, true},
		{"above maximum", `"12h1s"`, 0, true},
		{"negative duration", `"-40m"`, 0, true},
		{"fractional seconds", `"40m1ms"`, 0, true},
		{"missing unit", `"2400"`, 0, true},
		{"invalid unit", `"40min"`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := base
			if tc.value != "" {
				text = strings.Replace(text, "  config-file:\n", "  config-file:\n    stsDuration: "+tc.value+"\n", 1)
			}
			if err := os.WriteFile(file, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(file)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "stsDuration") || !strings.Contains(err.Error(), "config-file") {
					t.Fatalf("expected scene stsDuration error, got %v", err)
				}
				if cfg != nil {
					t.Fatal("invalid configuration returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Scenes["config-file"].STSDuration; got != tc.want {
				t.Fatalf("stsDuration = %v, want %v", got, tc.want)
			}
		})
	}
}
