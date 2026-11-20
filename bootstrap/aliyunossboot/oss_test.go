package aliyunossboot

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bpcoder16/pixiu/infra/aliyun/ossx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func TestNamedBuckets(t *testing.T) {
	// Pixiu 的注册表关闭后不能重开，分别在新进程验证正常退出和部分初始化失败。
	mode := os.Getenv("BAIZE_OSS_BOOT_TEST")
	if mode == "" {
		for _, mode := range []string{"success", "bad-bucket"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNamedBuckets$")
				cmd.Env = append(os.Environ(), "BAIZE_OSS_BOOT_TEST="+mode)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			})
		}
		return
	}
	dir := t.TempDir()
	names := []string{"public", "private"}
	const endpoint = "http://127.0.0.1:1"
	for _, name := range names {
		bucket := "example-" + name
		if mode == "bad-bucket" && name == "private" {
			bucket = "invalid/bucket"
		}
		body := fmt.Sprintf(`region: cn-hangzhou
bucket: %q
accessKeyID: test-id
accessKeySecret: test-secret
endpoint: %q
requestTimeout: 2s
slowThreshold: 100ms
logRequests: false
`, bucket, endpoint)
		if err := os.WriteFile(filepath.Join(dir, "aliyunossx."+name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var resources lifecycle.Stack
	defer resources.Close()
	err := Instances(names...).Init(dir, &resources)
	if mode == "bad-bucket" {
		if err == nil || !strings.Contains(err.Error(), "aliyunossx.private.yaml") {
			t.Fatalf("应报告无效 Bucket 的实例文件，实际为 %v", err)
		}
		names = names[:1]
	} else if err != nil {
		t.Fatal(err)
	}
	var clients []*ossx.Client
	for _, name := range names {
		client := ossx.Named(name)
		info := client.Info()
		if info.Name != name || info.Bucket != "example-"+name || info.Region != "cn-hangzhou" || info.Endpoint != endpoint {
			t.Errorf("实例 %q 的 Bucket 或连接配置有误", name)
		}
		clients = append(clients, client)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if _, err := client.PutObject(t.Context(), ossx.PutObjectRequest{}); !errors.Is(err, ossx.ErrClosed) {
			t.Fatalf("资源回收后仍可使用 OSS 客户端: %v", err)
		}
	}
}
