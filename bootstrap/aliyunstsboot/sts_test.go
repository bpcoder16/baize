package aliyunstsboot

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/pixiu/infra/aliyun/stsx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestNamedSTS(t *testing.T) {
	// 注册表按进程关闭，独立验证正常回收和第二个实例配置错误。
	mode := os.Getenv("BAIZE_STS_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "missing-secret"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNamedSTS$")
				cmd.Env = append(os.Environ(), "BAIZE_STS_BOOT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
		fmt.Fprintf(w, `{"Credentials":{"AccessKeyId":"temp-id","AccessKeySecret":"temp-secret","SecurityToken":"test-token","Expiration":%q}}`, expires)
	}))
	defer server.Close()
	dir := t.TempDir()
	names := []string{"oss", "backup"}
	for _, name := range names {
		secret := "test-secret"
		if mode == "missing-secret" && name == "backup" {
			secret = ""
		}
		body := fmt.Sprintf(`region: cn-hangzhou
accessKeyID: test-id
accessKeySecret: %q
endpoint: %q
requestTimeout: 5s
slowThreshold: 200ms
logRequests: false
`, secret, server.URL)
		if err := os.WriteFile(filepath.Join(dir, "aliyunstsx."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var resources lifecycle.Stack
	defer resources.Close()
	initErr := Instances(names...).Init(dir, &resources)
	if mode == "missing-secret" {
		if initErr == nil || !strings.Contains(initErr.Error(), "aliyunstsx.backup.yaml") {
			t.Fatalf("应报告凭据缺失的实例文件，实际为 %v", initErr)
		}
		names = names[:1]
	} else if initErr != nil {
		t.Fatal(initErr)
	}
	if calls.Load() != 0 {
		t.Fatal("初始化时向 STS 发出了请求")
	}
	req := stsx.AssumeRoleRequest{
		RoleARN:  "acs:ram::123456789012:role/test",
		Duration: time.Hour,
	}
	var clients []*stsx.Client
	for _, name := range names {
		client := stsx.Named(name)
		creds, err := client.AssumeRole(t.Context(), req)
		if err != nil || creds.SecurityToken != "test-token" {
			t.Fatalf("实例 %q 调用本地 STS 失败: %v", name, err)
		}
		clients = append(clients, client)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if _, err := client.AssumeRole(t.Context(), req); !errors.Is(err, stsx.ErrClosed) {
			t.Fatalf("资源回收后仍可使用 STS 客户端: %v", err)
		}
	}
}
