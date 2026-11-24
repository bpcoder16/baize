package elasticsearchboot_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/elasticsearchboot"
	esv7 "github.com/bpcoder16/baize/bootstrap/elasticsearchboot/v7"
	esv8 "github.com/bpcoder16/baize/bootstrap/elasticsearchboot/v8"
	esv9 "github.com/bpcoder16/baize/bootstrap/elasticsearchboot/v9"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/elasticsearchx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestInstances(t *testing.T) {
	// Pixiu 的命名注册表关闭后不能重开，每组验证在独立进程中执行。
	mode := os.Getenv("BAIZE_ELASTICSEARCH_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "version-mismatch"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInstances$")
				cmd.Env = append(os.Environ(), "BAIZE_ELASTICSEARCH_BOOT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := t.TempDir()
	names := []string{"legacy", "default", "events"}
	var clients []*elasticsearchx.Client
	if mode == "version-mismatch" {
		writeInstanceConfigs(t, dir, names)
		var resources lifecycle.Stack
		defer resources.Close()
		group := elasticsearchboot.Instances(esv7.Named("legacy"), esv8.Named("default", "events"))
		initErr := group.Init(dir, &resources)
		if initErr == nil || !strings.Contains(initErr.Error(), "elasticsearch.events.yaml") || !strings.Contains(initErr.Error(), "major mismatch") {
			t.Fatalf("版本不符应指出实例路径和原因，实际为 %v", initErr)
		}
		names = names[:2]
		clients = checkInstances(t, t.Context(), names, elasticsearchx.Named)
		if err := resources.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		clients = checkPlanInstances(t, dir, names)
	}
	for i, client := range clients {
		if _, err := client.Count(t.Context(), "products", map[string]any{}); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("资源栈未关闭 %q", names[i])
		}
	}
}

func checkPlanInstances(t *testing.T, dir string, names []string) []*elasticsearchx.Client {
	t.Helper()
	t.Chdir(t.TempDir())
	appPath := filepath.Join(dir, "app.yaml")
	app := "env: {appName: test, runMode: test, timeLocation: UTC, localIP: 127.0.0.1}\nlog: {format: text, hookStd: false}\n"
	if err := os.WriteFile(appPath, []byte(app), 0o600); err != nil {
		t.Fatal(err)
	}
	var clients []*elasticsearchx.Client
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{
			{
				Name: "test-elasticsearch-servers",
				Init: func(dir string, _ *lifecycle.Stack) error {
					writeInstanceConfigs(t, dir, names)
					return nil
				},
			},
			elasticsearchboot.Instances(esv7.Named("legacy"), esv8.Named("default"), esv9.Named("events")),
		},
		Prepare: []bootstrap.Step{{
			Name: "check-elasticsearch-instances",
			Init: func(ctx context.Context, _ string, _ *lifecycle.Stack) error {
				clients = checkInstances(t, ctx, names, func(name string) *elasticsearchx.Client {
					client, err := bootstrap.LookupDeclared(ctx, constant.ResourceElasticsearch, name, elasticsearchx.Named)
					if err != nil {
						t.Fatalf("借用 Elasticsearch 实例 %q 失败: %v", name, err)
					}
					return client
				})
				return nil
			},
		}},
	}
	if err := bootstrap.Run(t.Context(), appPath, plan); err != nil {
		t.Fatal(err)
	}
	return clients
}

func writeInstanceConfigs(t *testing.T, dir string, names []string) {
	t.Helper()
	for i, name := range names {
		major := 7 + i
		auth := "username: test\npassword: pass\n"
		wantAuth := "Basic dGVzdDpwYXNz"
		if major == 9 {
			auth = "apiKey: example-key\n"
			wantAuth = "APIKey example-key"
		}
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Elastic-Product", "Elasticsearch")
			w.Header().Set("Content-Type", "application/json")
			if r.Header.Get("Authorization") != wantAuth {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			switch r.URL.Path {
			case "/":
				fmt.Fprintf(w, `{"version":{"number":"%d.17.0","build_flavor":"default"},"tagline":"You Know, for Search"}`, major)
			case "/products/_count":
				fmt.Fprintf(w, `{"count":%d}`, major)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		certFile := name + ".pem"
		cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		if err := os.WriteFile(filepath.Join(dir, certFile), cert, 0o600); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("addresses: [%q]\ncaCertFile: %q\n%s", server.URL, certFile, auth)
		if err := os.WriteFile(filepath.Join(dir, "elasticsearch."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func checkInstances(t *testing.T, ctx context.Context, names []string, named func(string) *elasticsearchx.Client) []*elasticsearchx.Client {
	t.Helper()
	var clients []*elasticsearchx.Client
	query := map[string]any{}
	for i, name := range names {
		client := named(name)
		count, err := client.Count(ctx, "products", query)
		if err != nil || count != int64(7+i) {
			t.Fatalf("实例 %q 返回 %d, %v", name, count, err)
		}
		clients = append(clients, client)
	}
	return clients
}

func TestLegacyVersionKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "elasticsearch.default.yaml")
	if err := os.WriteFile(path, []byte("version: 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var resources lifecycle.Stack
	defer resources.Close()
	err := esv8.Instances("default").Init(dir, &resources)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "version") {
		t.Fatalf("旧 version 字段应触发配置错误，实际为 %v", err)
	}
}

func TestVersionPackageDependencies(t *testing.T) {
	versions := []string{"v7", "v8", "v9"}
	for _, selected := range versions {
		t.Run(selected, func(t *testing.T) {
			// 三个构造函数都能正确验活，只有检查生产依赖才能发现意外引入其他 SDK。
			cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "./"+selected)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go list: %v\n%s", err, out)
			}
			deps := strings.Fields(string(out))
			for _, version := range versions {
				found := slices.Contains(deps, "github.com/elastic/go-elasticsearch/"+version)
				if found != (version == selected) {
					t.Errorf("装配包 %s 的依赖中包含 SDK %s = %t", selected, version, found)
				}
			}
		})
	}
}
