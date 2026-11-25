package shareboot_test

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpcoder16/baize/bootstrap/shareboot"
	"github.com/bpcoder16/baize/service/share"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/gin-gonic/gin"
)

type pageRequest struct {
	ID int `json:"id"`
}

const page share.Page[pageRequest] = "example.v1"

func TestPrepareLifecycle(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("../../conf.example/baize.share.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const emptyKey = `key: ""`
	if !strings.Contains(string(data), emptyKey) {
		t.Fatal("expected an empty key in the example config")
	}
	cfg := strings.Replace(string(data), emptyKey, `key: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="`, 1)
	if err = os.WriteFile(filepath.Join(dir, "baize.share.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	step := shareboot.Prepare(page.Register(func(*gin.Context, pageRequest) {}))
	var closed lifecycle.Stack
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := step.Init(t.Context(), dir, &closed); !errors.Is(err, lifecycle.ErrClosed) {
		t.Fatalf("closed stack: %v", err)
	}
	if _, err := page.BuildURL("owner", pageRequest{}); !errors.Is(err, share.ErrUnavailable) {
		t.Fatalf("rollback: %v", err)
	}
	for range 2 {
		var stack lifecycle.Stack
		if err := step.Init(t.Context(), dir, &stack); err != nil {
			t.Fatal(err)
		}
		if _, err := page.BuildURL("owner", pageRequest{}); err != nil {
			t.Fatal(err)
		}
		if err := stack.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrepareWithoutPageURL(t *testing.T) {
	dir := t.TempDir()
	const cfg = `activeKeyID: k1
keys:
  - id: k1
    key: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
`
	if err := os.WriteFile(filepath.Join(dir, "baize.share.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	var got pageRequest
	step := shareboot.Prepare(page.Register(func(_ *gin.Context, req pageRequest) {
		got = req
	}))
	var stack lifecycle.Stack
	t.Cleanup(func() {
		if err := stack.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := step.Init(t.Context(), dir, &stack); err != nil {
		t.Fatal(err)
	}

	req := pageRequest{ID: 42}
	if link, err := page.BuildURL("owner", req); link != "" || !errors.Is(err, share.ErrMissingPageURL) {
		t.Fatalf("BuildURL: got %q, %v; want ErrMissingPageURL", link, err)
	}
	token, err := page.Build("owner", req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := share.Open(ctx, token); err != nil || got != req {
		t.Fatalf("Open: got %+v, %v; want %+v", got, err, req)
	}
}

func TestPrepareRejectsConfigBeforePublication(t *testing.T) {
	const config = "pageURL: 'https://site.example/s?lang=zh'\nactiveKeyID: k1\nkeys:\n  - id: k1\n    key: '{key}'\n"
	const key = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	// 即使环境变量存在，也只接受配置文件中的密钥值。
	t.Setenv("TEST_SHARE_KEY", key)
	for _, tc := range []struct{ name, config, key string }{
		{"empty pageURL", strings.Replace(config, "'https://site.example/s?lang=zh'", "''", 1), key},
		{"http", strings.Replace(config, "https:", "http:", 1), key},
		{"relative", strings.Replace(config, "https://site.example", "", 1), key},
		{"out-of-range port", strings.Replace(config, "site.example", "site.example:65536", 1), key},
		{"zero port", strings.Replace(config, "site.example", "[::1]:0", 1), key},
		{"credentials", strings.Replace(config, "site.example", "user@site.example", 1), key},
		{"fragment", strings.Replace(config, "lang=zh", "lang=zh#info", 1), key},
		{"query", strings.Replace(config, "lang=zh", "%73=x", 1), key},
		{"malformed query", strings.Replace(config, "lang=zh", "lang=%xy", 1), key},
		{"unknown active", strings.Replace(config, "activeKeyID: k1", "activeKeyID: k2", 1), key},
		{"unknown setting", config + "extra: 1\n", key},
		{"duplicate id", config + "  - id: k1\n    key: '{key}'\n", key},
		{"old keyEnv field", strings.Replace(config, "key: '{key}'", "keyEnv: TEST_SHARE_KEY", 1), key},
		{"environment variable name as value", config, "TEST_SHARE_KEY"},
		{"missing key", strings.Replace(config, "    key: '{key}'\n", "", 1), key},
		{"empty key", config, ""},
		{"non-base64", config, strings.Repeat("!", 44)},
		{"short key", config, "AQID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := strings.ReplaceAll(tc.config, "{key}", tc.key)
			if err := os.WriteFile(filepath.Join(dir, "baize.share.yaml"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			var stack lifecycle.Stack
			t.Cleanup(func() {
				if err := stack.Close(); err != nil {
					t.Error(err)
				}
			})
			err := shareboot.Prepare(page.Register(func(*gin.Context, pageRequest) {})).Init(t.Context(), dir, &stack)
			if err == nil || tc.key != "" && strings.Contains(err.Error(), tc.key) {
				t.Fatal("expected a safe configuration error")
			}
			if _, err := page.BuildURL("owner", pageRequest{}); !errors.Is(err, share.ErrUnavailable) {
				t.Fatalf("published invalid configuration: %v", err)
			}
		})
	}
}
