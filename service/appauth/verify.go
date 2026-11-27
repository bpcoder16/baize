package appauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bpcoder16/baize/httpauth"
	"github.com/bpcoder16/baize/service/appauth/internal/appsigv1"
)

var (
	ErrForbidden    = errors.New("appauth: permission denied")
	ErrBodyTooLarge = errors.New("appauth: body exceeds verification limit")
	ErrUnavailable  = errors.New("appauth: verifier unavailable")
)

const clockWindow = 2 * time.Minute

type identityKey struct{}

// AppIDFromContext 返回通过签名和权限校验的应用身份。
func AppIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(identityKey{}).(string)
	return id, ok
}

type credentials struct {
	appID, nonce string
	timestamp    int64
}

func (c credentials) fresh(now int64) bool {
	window := int64(clockWindow / time.Second)
	return c.timestamp >= now-window && c.timestamp <= now+window
}

// Authorize 验证原始入站请求并原子占用 nonce。成功时返回携带 AppID、正文可继续读取的请求。
func Authorize(r *http.Request, permission string) (*http.Request, error) {
	v := active.Load()
	if v == nil {
		return nil, ErrUnavailable
	}
	creds, err := v.verify(r, permission, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	// 慢请求可能在读完正文时已过期。
	if !creds.fresh(time.Now().Unix()) {
		return nil, httpauth.ErrInvalidCredentials
	}
	// 未来时间戳最多提前一个窗口到达，记录覆盖两个窗口及秒级边界。
	ttl := 2*clockWindow + time.Second
	ok, err := v.db.SetNX(r.Context(), v.keyPrefix+creds.appID+":"+creds.nonce, "1", ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: reserve nonce: %w", ErrUnavailable, err)
	}
	if !ok {
		return nil, httpauth.ErrInvalidCredentials
	}
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, creds.appID)), nil
}

func (v *verifier) verify(r *http.Request, permission string, now int64) (credentials, error) {
	var zero credentials
	headers := r.Header
	// 四个签名头都未提供时视为缺少凭证；提供了部分头或同一头有多个值时视为凭证无效。
	present := false
	for _, name := range []string{appsigv1.AppIDHeader, appsigv1.TimestampHeader, appsigv1.NonceHeader, appsigv1.SignatureHeader} {
		values := headers.Values(name)
		if len(values) > 1 {
			return zero, httpauth.ErrInvalidCredentials
		}
		present = present || len(values) == 1
	}
	if !present {
		return zero, httpauth.ErrMissingCredentials
	}
	id := headers.Get(appsigv1.AppIDHeader)
	stamp := headers.Get(appsigv1.TimestampHeader)
	nonce := headers.Get(appsigv1.NonceHeader)
	sig := headers.Get(appsigv1.SignatureHeader)
	c, known := v.clients[id]
	ts, err := strconv.ParseInt(stamp, 10, 64)
	creds := credentials{appID: id, nonce: nonce, timestamp: ts}
	if !known || err != nil || strconv.FormatInt(ts, 10) != stamp || !creds.fresh(now) {
		return zero, httpauth.ErrInvalidCredentials
	}
	signature := lowerHex(sig, sha256.Size)
	if signature == nil || lowerHex(nonce, 16) == nil || r.Method == http.MethodConnect ||
		!strings.HasPrefix(r.RequestURI, "/") || r.RequestURI != r.URL.RequestURI() {
		return zero, httpauth.ErrInvalidCredentials
	}
	if r.ContentLength > v.maxBodyBytes {
		return zero, ErrBodyTooLarge
	}
	var body []byte
	if r.Body != nil {
		limited := http.MaxBytesReader(nil, r.Body, v.maxBodyBytes)
		body, err = io.ReadAll(limited)
		_ = limited.Close()
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return zero, ErrBodyTooLarge
		}
		if err != nil {
			return zero, fmt.Errorf("%w: read request body: %w", httpauth.ErrInvalidCredentials, err)
		}
	}
	bodyHash := sha256.Sum256(body)
	if !c.mac.Verify(appsigv1.Message(id, stamp, nonce, r.Method, r.RequestURI, bodyHash[:]), signature) {
		return zero, httpauth.ErrInvalidCredentials
	}
	if !c.permissions[permission] {
		return zero, ErrForbidden
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return creds, nil
}

func lowerHex(s string, size int) []byte {
	if len(s) != size*2 || strings.ToLower(s) != s {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
