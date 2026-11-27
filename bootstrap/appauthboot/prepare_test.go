package appauthboot_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/appauthboot"
	"github.com/bpcoder16/baize/bootstrap/redisboot"
	"github.com/bpcoder16/baize/httpauth"
	"github.com/bpcoder16/baize/middleware/ginappauth"
	"github.com/bpcoder16/baize/service/appauth"
	"github.com/bpcoder16/baize/service/appauth/appsign"
	"github.com/bpcoder16/pixiu/infra/httpcall"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/gin-gonic/gin"
)

func TestAppAuthPlan(t *testing.T) {
	// Pixiu 的 env 全局实例每进程只允许初始化一次。
	const child = "BAIZE_TEST_APPAUTH_PLAN"
	if os.Getenv(child) != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAppAuthPlan$")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	dir := t.TempDir()
	template, err := os.ReadFile("../../conf.example/baize.appauth.yaml")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	store := miniredis.RunT(t)
	write("app.yaml", "env: {appName: profiles, runMode: test, timeLocation: UTC, localIP: 127.0.0.1}\nlog: {format: text, hookStd: false}\n")
	write("redis.cache.yaml", fmt.Sprintf("host: %s\nport: %s\nmaxRetries: -1\n", store.Host(), store.Port()))
	key := strings.Repeat("ab", 32)
	write("baize.appauth.yaml", strings.Replace(string(template), `appSecret: ""`, `appSecret: "`+key+`"`, 1))
	client, err := appsign.NewClient("profiles", "order-service", key)
	if err != nil {
		t.Fatal(err)
	}

	prepare := appauthboot.Prepare("cache")
	plan := bootstrap.Plan{
		Resources: []bootstrap.Resource{redisboot.Instances("cache")},
		Prepare: []bootstrap.Step{prepare, {Name: "check", Init: func(ctx context.Context, _ string, stack *lifecycle.Stack) error {
			if err := appauthboot.Prepare("undeclared").Init(ctx, dir, stack); err == nil {
				t.Fatal("允许借用未声明的 Redis")
			}
			checkRoutes(t, ctx, client, store)
			return nil
		}}},
	}
	if err := bootstrap.Run(t.Context(), filepath.Join(dir, "app.yaml"), plan); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/users", nil)
	if _, err := appauth.Authorize(r, "users.read"); !errors.Is(err, appauth.ErrUnavailable) {
		t.Fatalf("启动计划退出后仍有验证器：%v", err)
	}
}

func checkRoutes(t *testing.T, ctx context.Context, client *httpcall.Client, store *miniredis.Miniredis) {
	t.Helper()
	router := gin.New()

	const body = `{"userIds":[42]}`
	router.POST("/users", ginappauth.Require("users.read", nil), func(c *gin.Context) {
		id, ok := appauth.AppIDFromContext(c.Request.Context())
		if !ok || id != "order-service" {
			t.Errorf("预期已验证的 order-service，得到 %q", id)
		}
		if _, ok := httpauth.PrincipalFromContext(c.Request.Context()); ok {
			t.Error("应用身份覆盖了终端用户身份")
		}
		data, err := io.ReadAll(c.Request.Body)
		if err != nil || string(data) != body {
			t.Error("handler 未收到签名时的原始正文")
		}

		c.Status(200)
	})
	router.POST("/private", ginappauth.Require("other.permission", func(c *gin.Context, err error) {
		if !errors.Is(err, appauth.ErrForbidden) {
			t.Errorf("预期权限拒绝，得到 %v", err)
		}
		c.JSON(200, gin.H{"code": 10004})
	}), func(*gin.Context) { t.Error("无权限的请求进入了 handler") })

	// 取得实际出站请求的头，用同一份签名验证正文篡改与并发重放。
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer capture.Close()
	makeHeaders := func(target string) http.Header {
		t.Helper()
		resp, err := client.Request(ctx).SetBody(body).Post(capture.URL + target)
		if err != nil {
			t.Fatal(err)
		}
		return resp.Request.RawRequest.Header.Clone()
	}
	call := func(target, payload string, h http.Header) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", target, strings.NewReader(payload))
		r.Header = h.Clone()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	const target = "/users?tag=a&tag=b"
	headers := makeHeaders(target)
	if w := call(target, body+" ", headers); w.Code != 401 {
		t.Fatal("被篡改的正文通过鉴权")
	}
	if w := call("/users?tag=b&tag=a", body, headers); w.Code != 401 {
		t.Fatal("被篡改的查询串通过鉴权")
	}
	if w := call(target, body, nil); w.Code != 401 {
		t.Fatal("缺少签名时未拒绝")
	}
	dup := headers.Clone()
	dup.Add("X-Nonce", dup.Get("X-Nonce"))
	if w := call(target, body, dup); w.Code != 401 {
		t.Fatal("重复请求头未拒绝")
	}
	if w := call("/private", body, makeHeaders("/private")); w.Body.String() != `{"code":10004}` {
		t.Fatal("未使用宿主的失败处理")
	}
	if len(store.Keys()) != 0 {
		t.Fatal("鉴权失败占用了 nonce")
	}
	var group sync.WaitGroup
	results := make(chan int, 12)
	for range cap(results) {
		group.Go(func() { results <- call(target, body, headers).Code })
	}
	group.Wait()
	close(results)
	passed := 0
	for code := range results {
		switch code {
		case 200:
			passed++
		case 401:
		default:
			t.Fatalf("意外的响应 %d", code)
		}
	}
	if passed != 1 {
		t.Fatalf("同一签名通过 %d 次", passed)
	}
	keys := store.Keys()
	wantKey := "baize:appauth:v1:profiles:order-service:" + headers.Get("X-Nonce")
	if len(keys) != 1 || keys[0] != wantKey {
		t.Fatalf("防重放键 = %v，预期 [%s]", keys, wantKey)
	}
	if ttl := store.TTL(wantKey); ttl != 241*time.Second {
		t.Fatalf("nonce TTL = %v，预期 241 秒", ttl)
	}
	store.SetError("ERR unavailable")
	if w := call(target, body, makeHeaders(target)); w.Code != 503 {
		t.Fatal("Redis 故障时未关闭访问")
	}
}
