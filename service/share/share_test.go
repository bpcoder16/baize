package share_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpcoder16/baize/service/share"
	"github.com/bpcoder16/pixiu/cryptox"
	"github.com/gin-gonic/gin"
)

type productRequest struct {
	ProductID string `json:"productId" binding:"required"`
	Revision  int64  `json:"revision"`
	Locale    string `json:"locale,omitempty"` // 旧链接没有此字段
}

type productV2Request struct {
	SKU string `json:"sku" binding:"required"`
}

const product share.Page[productRequest] = "product.detail.v1"
const productV2 share.Page[*productV2Request] = "product.detail.v2"

// 使用独立 AES-GCM 实现和固定测试密钥生成，时间为 2000 年。不要用当前编码器刷新样本。
const historicalURL = "https://old.example/share?s=s1.k1.v1.ICEiIyQlJicoKSoryNtczRyHvwoyrVbUrOORe-WHSCzby2YwFXPFa-VsjbaIUS8YsglYBALgZcCw-nnkqlHYnCyozt7eDh_S53S3izqqs3OT795eD9Pk2mn5lwjMlAXuSYC5vZ63Vzvou8TS7QgAmY5MJLq_RI1bNzVa815RcGid3FryYn4L5KLlpJndHNcJ"

type testKey struct {
	id   string
	fill byte
}

var (
	k1 = testKey{id: "k1", fill: 1}
	k2 = testKey{id: "k2", fill: 2}
)

func configFile(t *testing.T, active string, keys ...testKey) string {
	t.Helper()
	cfg := "pageURL: 'https://[2001:db8::1]:65535/share?lang=zh'\nactiveKeyID: " + active + "\nkeys:\n"
	for _, k := range keys {
		value := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{k.fill}, 32))
		cfg += fmt.Sprintf("  - id: %s\n    key: %q\n", k.id, value)
	}
	path := filepath.Join(t.TempDir(), "baize.share.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func activate(t *testing.T, file string, registrations ...share.Registration) func() {
	t.Helper()
	release, err := share.Load(file, registrations...)
	if err != nil {
		t.Fatal(err)
	}
	closeService := func() {
		t.Helper()
		if err := release(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(closeService)
	return closeService
}

func tokenFrom(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Query().Get("s") == "" {
		t.Fatalf("invalid link: %q", link)
	}
	return u.Query().Get("s")
}

func assertOpen(t *testing.T, token, want string) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	err := share.Open(c, token)
	if want == "" {
		if !errors.Is(err, share.ErrInvalidShare) || c.Writer.Written() {
			t.Fatalf("invalid dispatch: %v", err)
		}
	} else if err != nil || w.Body.String() != want {
		t.Fatalf("open: %v, response=%q, want=%q", err, w.Body.String(), want)
	}
}

func TestVersionAndKeyRollout(t *testing.T) {
	original := configFile(t, "k1", k1)
	prepared := configFile(t, "k1", k1, k2)
	current := configFile(t, "k2", k1, k2)
	retired := configFile(t, "k2", k2)
	r1 := product.Register(func(c *gin.Context, req productRequest) {
		c.String(200, "%s/%d/%s", req.ProductID, req.Revision, req.Locale)
	})
	r2 := productV2.Register(func(c *gin.Context, req *productV2Request) {
		c.String(200, "%s", req.SKU)
	})
	old := tokenFrom(t, historicalURL)
	const oldResult = "sku-123/9007199254740993/"
	stop := activate(t, original, r1)
	assertOpen(t, old, oldResult)
	stop()

	// 先全部支持新版读取，再启用新版签发。
	stop = activate(t, prepared, r1, r2)
	assertOpen(t, old, oldResult)
	stop()
	stop = activate(t, current, r1, r2)
	link, err := productV2.BuildURL("merchant:00042", &productV2Request{SKU: "new"})
	if err != nil {
		t.Fatal(err)
	}
	newToken := tokenFrom(t, link)
	if !strings.HasPrefix(newToken, "s1.k2.v1.") {
		t.Fatal("wrong active key")
	}
	assertOpen(t, old, oldResult)
	assertOpen(t, newToken, "new")
	stop()

	// 回滚到预先发布的读取版本后，两代链接依旧可用。
	stop = activate(t, prepared, r1, r2)
	assertOpen(t, old, oldResult)
	assertOpen(t, newToken, "new")
	stop()
	// 旧副本已得到 k2，但尚未注册 v2，只能打开 v1。
	stop = activate(t, prepared, r1)
	assertOpen(t, old, oldResult)
	assertOpen(t, newToken, "")
	stop()
	stop = activate(t, retired, r1, r2)
	assertOpen(t, old, "")
	assertOpen(t, newToken, "new")
	stop()

	// 两个 kid 仍在，若部署系统覆盖了 k1 的原始密钥，只有旧链接失效。
	overwritten := configFile(t, "k2", testKey{id: "k1", fill: 3}, k2)
	stop = activate(t, overwritten, r1, r2)
	assertOpen(t, old, "")
	assertOpen(t, newToken, "new")
	stop()
	if err := share.Open(nil, old); !errors.Is(err, share.ErrUnavailable) {
		t.Fatalf("after release: %v", err)
	}
}

func TestSamePageRequestAcrossDeployments(t *testing.T) {
	type originalRequest struct {
		SKU string `json:"sku" binding:"required"`
	}
	type updatedRequest struct {
		SKU    string `json:"sku" binding:"required"`
		Source string `json:"source,omitempty"` // 只用于统计，旧版忽略时页面结果一致。
	}
	const original share.Page[originalRequest] = "catalog.v1"
	const updated = share.Page[updatedRequest](original)
	file := configFile(t, "k1", k1)
	oldReader := original.Register(func(c *gin.Context, req originalRequest) {
		c.String(http.StatusOK, "%s", req.SKU)
	})
	var source string
	newReader := updated.Register(func(c *gin.Context, req updatedRequest) {
		source = req.Source
		c.String(http.StatusOK, "%s", req.SKU)
	})

	stop := activate(t, file, oldReader)
	oldLink, err := original.BuildURL("shop", originalRequest{SKU: "book"})
	if err != nil {
		t.Fatal(err)
	}
	stop()

	stop = activate(t, file, newReader)
	assertOpen(t, tokenFrom(t, oldLink), "book")
	if source != "" {
		t.Fatalf("old link supplied source %q", source)
	}
	newLink, err := updated.BuildURL("shop", updatedRequest{
		SKU:    "album",
		Source: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	newToken := tokenFrom(t, newLink)
	assertOpen(t, newToken, "album")
	if source != "catalog" {
		t.Fatalf("new reader lost source: %q", source)
	}
	stop()
	activate(t, file, oldReader)
	assertOpen(t, newToken, "album")
}

func TestDynamicNumbersPreserveWireValue(t *testing.T) {
	type request struct {
		Metadata map[string]any `json:"metadata"`
	}
	const page share.Page[request] = "details.v1"
	const revision int64 = 9007199254740993
	activate(t, configFile(t, "k1", k1), page.Register(func(c *gin.Context, req request) {
		if got := req.Metadata["revision"]; got != json.Number("9007199254740993") {
			t.Errorf("revision = %v (%T)", got, got)
		}
		amount := req.Metadata["amounts"].([]any)[0]
		if amount != json.Number("1.2300") {
			t.Errorf("amount = %v (%T)", amount, amount)
		}
		c.String(http.StatusOK, "ok")
	}))
	link, err := page.BuildURL("shop", request{Metadata: map[string]any{
		"revision": revision,
		"amounts":  []json.Number{"1.2300"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	assertOpen(t, tokenFrom(t, link), "ok")
}

func TestURLAndGinDispatch(t *testing.T) {
	request := productRequest{ProductID: "real", Revision: 9007199254740993}
	var count int
	activate(t, configFile(t, "v1", testKey{id: "v1", fill: 1}), product.Register(func(c *gin.Context, got productRequest) {
		count++
		if got != request || c.GetString("visitor") != "guest" {
			t.Fatalf("wrong dispatch: %+v", got)
		}
		c.String(200, "shown")
	}))
	before := time.Now().Unix()
	link, err := product.BuildURL("merchant:00042", request)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	if u.Scheme != "https" || u.Host != "[2001:db8::1]:65535" || u.Path != "/share" || u.Query().Get("lang") != "zh" || len(u.Query()["s"]) != 1 {
		t.Fatalf("page URL: %s", link)
	}
	second, err := product.BuildURL("merchant:00042", request)
	if err != nil || link == second {
		t.Fatalf("fresh nonce: %v", err)
	}
	token := tokenFrom(t, link)
	if !strings.HasPrefix(token, "s1.v1.v1.") {
		t.Fatal("wrong share token prefix")
	}
	box, err := cryptox.NewAESGCM(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	body, err := box.DecryptToken(strings.TrimPrefix(token, "s1.v1."), "baize.share/s1/v1")
	var payload struct {
		CreatedAt   int64          `json:"t"`
		OwnerID     string         `json:"o"`
		HandlerName string         `json:"p"`
		Request     productRequest `json:"r"`
	}
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.OwnerID != "merchant:00042" || payload.HandlerName != string(product) || payload.CreatedAt < before || payload.CreatedAt > time.Now().Unix() || payload.Request != request {
		t.Fatalf("payload: %+v, %v", payload, err)
	}

	r := gin.New()
	r.GET("/share", func(c *gin.Context) {
		c.Set("visitor", "guest")
		tokens := c.QueryArray("s")
		if len(tokens) != 1 || share.Open(c, tokens[0]) != nil {
			c.Status(http.StatusBadRequest)
		}
	})
	for _, check := range []struct {
		url    string
		status int
	}{
		{link + "&productId=other", 200},
		{link + "&s=" + token, 400},
		{strings.Replace(link, "s=s1.v1.", "s=s1.k2.", 1), 400},
		{link[:len(link)-4], 400},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", check.url, nil))
		if w.Code != check.status {
			t.Fatalf("status %d, want %d", w.Code, check.status)
		}
	}
	if count != 1 {
		t.Fatalf("handler ran %d times", count)
	}
}

func TestBuildChecksRegisteredTypeAndWireValue(t *testing.T) {
	type inputOnly struct {
		Value string `json:"-" binding:"required"`
	}
	const input share.Page[inputOnly] = "input"
	activate(t, configFile(t, "k1", k1), input.Register(func(*gin.Context, inputOnly) {}), productV2.Register(func(*gin.Context, *productV2Request) {}))
	check := func(want error, link string, err error) {
		t.Helper()
		if link != "" || !errors.Is(err, want) {
			t.Fatalf("got %q, %v; want %v", link, err, want)
		}
	}
	link, err := input.BuildURL("owner", inputOnly{Value: "discarded"})
	check(share.ErrInvalidRequest, link, err)
	link, err = productV2.BuildURL("owner", nil)
	check(share.ErrInvalidRequest, link, err)
	link, err = share.Page[productRequest](productV2).BuildURL("owner", productRequest{})
	check(share.ErrUnregistered, link, err)
	link, err = product.BuildURL("owner", productRequest{})
	check(share.ErrUnregistered, link, err)
	for _, owner := range []string{"", " owner", "foo\nbar", string([]byte{0xff}), strings.Repeat("x", 129)} {
		link, err = productV2.BuildURL(owner, &productV2Request{SKU: "valid"})
		check(share.ErrInvalidRequest, link, err)
	}
	link, err = productV2.BuildURL("owner", &productV2Request{SKU: strings.Repeat("x", 2000)})
	check(share.ErrURLTooLong, link, err)
}

func TestRegistrationAndConcurrentDispatch(t *testing.T) {
	file := configFile(t, "k1", k1)
	r := productV2.Register(func(c *gin.Context, req *productV2Request) {
		c.String(200, "%s", req.SKU)
		req.SKU = "changed"
	})
	for _, regs := range [][]share.Registration{
		nil,
		{r, r},
		{product.Register(nil)},
		{share.Page[int]("bad").Register(func(*gin.Context, int) {})},
		{share.Page[productRequest]("bad name").Register(func(*gin.Context, productRequest) {})},
	} {
		if _, err := share.Load(file, regs...); err == nil {
			t.Fatal("accepted invalid registration")
		}
	}
	activate(t, file, r)
	if _, err := share.Load(file, r); err == nil {
		t.Fatal("replaced active service")
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			sku := fmt.Sprint(i)
			link, err := productV2.BuildURL("owner", &productV2Request{SKU: sku})
			if err != nil {
				t.Error(err)
				return
			}
			u, _ := url.Parse(link)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			if err := share.Open(c, u.Query().Get("s")); err != nil || w.Body.String() != sku {
				t.Errorf("concurrent open %s: %v", sku, err)
			}
		})
	}
	wg.Wait()
}

func TestOpenRejectsInvalidEnvelopeBeforeDispatch(t *testing.T) {
	activate(t, configFile(t, "k1", k1), product.Register(func(*gin.Context, productRequest) {
		t.Error("invalid envelope reached handler")
	}))
	box, err := cryptox.NewAESGCM(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const valid = `{"t":1,"o":"shop","p":"product.detail.v1","r":{"productId":"abc"}}`
	const oldFields = `{"createdAt":1,"ownerId":"shop","handlerName":"product.detail.v1","request":{"productId":"abc"}}`
	for _, tc := range []struct{ name, body, purpose string }{
		{"old fields", oldFields, "baize.share/s1/k1"},
		{"missing time", strings.Replace(valid, `"t":1,`, "", 1), "baize.share/s1/k1"},
		{"missing source", strings.Replace(valid, `"shop"`, `""`, 1), "baize.share/s1/k1"},
		{"unregistered page", strings.Replace(valid, ".v1", ".v9", 1), "baize.share/s1/k1"},
		{"null request", strings.Replace(valid, `{"productId":"abc"}`, "null", 1), "baize.share/s1/k1"},
		{"invalid request", strings.Replace(valid, `{"productId":"abc"}`, "{}", 1), "baize.share/s1/k1"},
		{"wrong kid binding", valid, "baize.share/s1/k2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ciphertext, err := box.EncryptToken([]byte(tc.body), tc.purpose)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if err := share.Open(c, "s1.k1."+ciphertext); !errors.Is(err, share.ErrInvalidShare) || c.Writer.Written() {
				t.Fatalf("Open: %v", err)
			}
		})
	}
}

func TestBuildTokenAtSizeLimit(t *testing.T) {
	activate(t, configFile(t, "k1", k1), productV2.Register(func(c *gin.Context, req *productV2Request) {
		c.String(http.StatusOK, "%s", req.SKU)
	}))
	// 当前协议下，1434 个 ASCII 字节的 SKU 使这组参数生成的 token 恰好占满 2048 字节。
	req := &productV2Request{SKU: strings.Repeat("x", 1434)}
	token, err := productV2.Build("owner", req)
	if err != nil || len(token) != 2048 {
		t.Fatalf("Build: len=%d, err=%v", len(token), err)
	}
	assertOpen(t, token, req.SKU)

	req.SKU += "x"
	token, err = productV2.Build("owner", req)
	if token != "" || !errors.Is(err, share.ErrURLTooLong) {
		t.Fatalf("oversized token: len=%d, err=%v", len(token), err)
	}
}

func TestFullURLBudgetIncludesFixedQuery(t *testing.T) {
	path := configFile(t, "k1", k1)
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := product.Register(func(*gin.Context, productRequest) {})
	req := productRequest{ProductID: "sku"}
	stop := activate(t, path, r)
	short, err := product.BuildURL("owner", req)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	for _, size := range []int{2048, 2049} {
		padded := strings.Replace(string(input), "lang=zh", "lang=zh"+strings.Repeat("x", size-len(short)), 1)
		if err := os.WriteFile(path, []byte(padded), 0600); err != nil {
			t.Fatal(err)
		}
		release := activate(t, path, r)
		stop() // 迟到的旧回收不得影响新实例
		link, err := product.BuildURL("owner", req)
		if size == 2048 && (err != nil || len(link) != size) || size > 2048 && (link != "" || !errors.Is(err, share.ErrURLTooLong)) {
			t.Fatalf("size %d: len=%d, err=%v", size, len(link), err)
		}
		if _, err := product.Build("owner", req); err != nil {
			t.Fatalf("Build with fixed query for URL size %d: %v", size, err)
		}
		release()
	}
}
