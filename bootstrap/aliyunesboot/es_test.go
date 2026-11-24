package aliyunesboot

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

	"github.com/bpcoder16/pixiu/infra/aliyun/esx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestNamedAliyunES(t *testing.T) {
	// CloseAll 会关闭进程级注册表，两个场景各使用独立进程。
	mode := os.Getenv("BAIZE_ALIYUN_ES_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "missing-instance-id"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNamedAliyunES$")
				cmd.Env = append(os.Environ(), "BAIZE_ALIYUN_ES_BOOT_TEST="+mode)
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
		id, ok := strings.CutPrefix(r.URL.Path, "/openapi/instances/")
		if r.Method != http.MethodGet || !ok {
			t.Errorf("意外的管理 API 请求 %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"RequestId":"test","Result":{"instanceId":%q,"status":"active","esVersion":"8.17"}}`, id)
	}))
	defer server.Close()

	dir := t.TempDir()
	names := []string{"search-control", "backup"}
	for _, name := range names {
		id := "es-" + name
		if mode == "missing-instance-id" && name == "backup" {
			id = ""
		}
		body := fmt.Sprintf(`region: cn-hangzhou
instanceID: %q
accessKeyID: test-key
accessKeySecret: test-secret
endpoint: %q
`, id, server.URL)
		if err := os.WriteFile(filepath.Join(dir, "aliyunesx."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var resources lifecycle.Stack
	defer resources.Close()
	initErr := Instances(names...).Init(dir, &resources)
	if mode == "missing-instance-id" {
		if initErr == nil || !strings.Contains(initErr.Error(), "aliyunesx.backup.yaml") {
			t.Fatalf("缺少云实例 ID 时应报告对应配置文件，实际为 %v", initErr)
		}
		names = names[:1]
	} else if initErr != nil {
		t.Fatal(initErr)
	}
	if calls.Load() != 0 {
		t.Fatal("初始化时向阿里云 ES 发出了请求")
	}
	var clients []*esx.Client
	for _, name := range names {
		client := esx.Named(name)
		result, err := client.DescribeInstance(t.Context())
		if err != nil || result.InstanceID != "es-"+name || result.Status != "active" {
			t.Fatalf("实例 %q 的查询结果为 %+v，错误为 %v", name, result, err)
		}
		clients = append(clients, client)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if _, err := client.DescribeInstance(t.Context()); !errors.Is(err, esx.ErrClosed) {
			t.Fatalf("资源回收后应返回 ErrClosed，实际为 %v", err)
		}
	}
}
