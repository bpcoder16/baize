package httpboot_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpcoder16/baize/bootstrap/httpboot"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/infra/ginx"
	"github.com/gin-gonic/gin"
	"github.com/swaggo/swag"
)

func TestGin(t *testing.T) {
	// 每种模式独立初始化一次进程环境和 Swag 注册表。
	mode := os.Getenv("BAIZE_TEST_HTTP_MODE")
	if mode == "" {
		for _, mode := range []string{env.RunModeDebug, env.RunModeTest, env.RunModeRelease} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGin$")
				cmd.Env = append(os.Environ(), "BAIZE_TEST_HTTP_MODE="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := t.TempDir()
	if err := env.Init(env.Config{
		AppName:       "test",
		RunMode:       mode,
		TimeLocation:  "UTC",
		ConfigDirPath: dir,
		LocalIP:       "127.0.0.1",
	}); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var engine *gin.Engine
	calls := 0
	decl := httpboot.Gin(func(r *gin.Engine) {
		calls++
		engine = r
		r.GET("/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}, ginx.OpenAPIModule{Name: "app", Title: "App"})
	if decl.Name != "http" || calls != 0 {
		t.Fatalf("声明服务时名称 %q，注册路由 %d 次", decl.Name, calls)
	}
	if _, err := decl.Build(t.Context(), dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("缺少配置时得到 %v", err)
	}
	write("http.yaml", "addr: 127.0.0.1:0\n")
	write("gin.yaml", "unknown: true\n")
	if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "gin.yaml") {
		t.Fatalf("未知 Gin 配置字段得到 %v", err)
	}
	write("gin.yaml", "disableAccessLog: true\n")
	if mode != env.RunModeRelease {
		if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "app") {
			t.Fatalf("缺少命名文档时得到 %v", err)
		}
		swag.Register("app", &swag.Spec{SwaggerTemplate: `{"swagger":"2.0","paths":{}}`})
	}
	write("http.yaml", "readHeaderTimeout: -1s\n")
	if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "http.yaml") {
		t.Fatalf("无效的 HTTP 超时得到 %v", err)
	}
	write("http.yaml", "addr: 127.0.0.1:0\n")
	write("gin.yaml", "cors:\n  - pathPrefix: /probe\n    allowMethods: [GET]\n")
	if _, err := decl.Build(t.Context(), dir); err == nil || !strings.Contains(err.Error(), "gin.yaml") || !strings.Contains(err.Error(), "allowOrigins") {
		t.Fatalf("跨域规则错误缺少配置位置: %v", err)
	}
	write("gin.yaml", `disableAccessLog: true
cors:
  - pathPrefix: /probe
    allowOrigins: ["https://web.example.com"]
    allowMethods: [GET]
    maxAge: 10m
`)
	service, err := decl.Build(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || engine == nil || gin.Mode() != mode {
		t.Fatalf("注册次数 %d，Gin 模式 %s", calls, gin.Mode())
	}
	req := httptest.NewRequest(http.MethodOptions, "/probe", nil)
	req.Header.Set("Origin", "https://web.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	preflight := httptest.NewRecorder()
	engine.ServeHTTP(preflight, req)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("预检响应 %d %v", preflight.Code, preflight.Header())
	}
	for _, path := range []string{"/probe", "/docs", "/docs/app", "/openapi/app.json"} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		want := http.StatusOK
		if path == "/probe" {
			want = http.StatusNoContent
		} else if mode == env.RunModeRelease {
			want = http.StatusNotFound
		}
		if w.Code != want {
			t.Errorf("GET %s = %d，预期 %d", path, w.Code, want)
		}
	}
	// 启动与停机交错时，Stop 可能先于 Run 进入。
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(); err != nil {
		t.Fatalf("停收后启动得到 %v", err)
	}
}
