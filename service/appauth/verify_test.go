package appauth

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestSignatureTimeWindow(t *testing.T) {
	const now int64 = 1_700_000_000
	for _, tc := range []struct {
		offset int64
		fresh  bool
	}{
		{-121, false},
		{-120, true},
		{120, true},
		{121, false},
	} {
		creds := credentials{timestamp: now + tc.offset}
		if got := creds.fresh(now); got != tc.fresh {
			t.Errorf("时间偏差 %+d 秒：fresh = %t，预期 %t", tc.offset, got, tc.fresh)
		}
	}
}

func TestConfiguration(t *testing.T) {
	db := redis.NewClient(&redis.Options{MaxRetries: -1})
	defer db.Close()
	file := filepath.Join(t.TempDir(), "baize.appauth.yaml")
	key := strings.Repeat("ab", 32)
	row := "  - {appID: demo, appSecret: \"" + key + "\", permissions: [users.read]}\n"
	for name, content := range map[string]string{
		"duplicate AppID":   "clients:\n" + row + row,
		"empty appSecret":   "clients:\n" + strings.Replace(row, key, "", 1),
		"missing appSecret": "clients:\n" + strings.Replace(row, "appSecret: \""+key+"\", ", "", 1),
		"invalid AppID":     "clients:\n" + strings.ReplaceAll(row, "demo", "bad id"),
		"unknown field":     "clients:\n" + row + "extra: true\n",
		"negative limit":    "maxBodyBytes: -1\nclients:\n" + row,
		"blank permission":  "clients:\n" + strings.ReplaceAll(row, "users.read", "''"),
		"invalid hex":       "clients:\n" + strings.Replace(row, key, strings.Repeat("zz", 32), 1),
		"legacy secretEnv":  "clients:\n" + strings.Replace(row, "appSecret:", "secretEnv:", 1),
	} {
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(file, "profiles", db); err == nil || active.Load() != nil {
			t.Fatalf("%s：错误配置已发布", name)
		}
	}
	if err := os.WriteFile(file, []byte("clients: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, retries := range []int{0, 1} {
		retryingDB := redis.NewClient(&redis.Options{MaxRetries: retries})
		_, err := Load(file, "profiles", retryingDB)
		_ = retryingDB.Close()
		if err == nil || active.Load() != nil {
			t.Fatalf("maxRetries=%d 时应拒绝装配验证器", retries)
		}
	}
	disable, err := Load(file, "profiles", db)
	if err != nil {
		t.Fatalf("停用全部调用方：%v", err)
	}
	disable()
	if err := os.WriteFile(file, []byte("maxBodyBytes: 2\nclients:\n"+row), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := Load(file, "profiles", db)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r := httptest.NewRequest("POST", "/users", strings.NewReader("abc"))
	r.ContentLength = -1 // 分块发送的超长正文同样受限。
	ts := time.Now().Unix()
	r.Header.Set("X-App-Id", "demo")
	r.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
	r.Header.Set("X-Nonce", strings.Repeat("0", 32))
	r.Header.Set("X-Signature", strings.Repeat("0", 64))
	if _, err := active.Load().verify(r, "users.read", ts); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("超长请求结果 %v", err)
	}
}
