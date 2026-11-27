package appsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/pixiu/infra/httpcall"
	"github.com/go-resty/resty/v2"
)

func TestSignedHTTPCall(t *testing.T) {
	const target = "/v1/a%2Fb?tag=b&tag=a"
	for _, tc := range []struct {
		method, payload string
		body            any
	}{
		{"GET", "", nil},
		{"POST", `{"id":42}`, map[string]int{"id": 42}},
		{"PUT", "\x00\xff", []byte{0, 255}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			start := time.Now().Unix()
			nonces := make(chan string, 2)
			key := strings.Repeat("ab", 32)
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != tc.payload || r.RequestURI != target || r.Method != tc.method {
					t.Errorf("unexpected wire request: %s %s %q, %v", r.Method, r.RequestURI, body, err)
				}
				ts, nonce := r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce")
				seconds, err := strconv.ParseInt(ts, 10, 64)
				if err != nil || seconds < start || seconds > time.Now().Unix() {
					t.Errorf("timestamp %q", ts)
				}
				rawNonce, err := hex.DecodeString(nonce)
				if err != nil || len(rawNonce) != 16 {
					t.Errorf("nonce %q", nonce)
				}
				rawKey, _ := hex.DecodeString(key)
				mac := hmac.New(sha256.New, rawKey)
				fmt.Fprintf(mac, "example\n%s\n%s\n%s\n%s\n%x", ts, nonce, r.Method, r.RequestURI, sha256.Sum256(body))
				if r.Header.Get("X-App-Id") != "example" || r.Header.Get("X-Signature") != hex.EncodeToString(mac.Sum(nil)) {
					t.Error("signature did not verify against received request")
				}
				nonces <- nonce
				if attempts.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			client, err := NewClient("profiles", "example", key, httpcall.OptResty(func(c *resty.Client) {
				c.SetBaseURL(server.URL).SetRetryCount(1).SetRetryWaitTime(time.Millisecond)
				c.AddRetryCondition(func(r *resty.Response, _ error) bool { return r.StatusCode() == http.StatusServiceUnavailable })
			}))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Request(t.Context()).SetBody(tc.body).Execute(tc.method, target)
			if err != nil || resp.StatusCode() != http.StatusOK || attempts.Load() != 2 {
				t.Fatalf("response=%v err=%v attempts=%d", resp, err, attempts.Load())
			}
			if <-nonces == <-nonces {
				t.Error("retry reused the same nonce")
			}
		})
	}
}

func TestRejectInvalidCredentials(t *testing.T) {
	key := strings.Repeat("01", 32)
	for _, c := range []struct{ appID, secret string }{
		{"", key}, {"a\nb", key}, {"app id", key}, {"app", "abcd"}, {"app", strings.Repeat("xy", 32)},
	} {
		if _, err := NewClient("profiles", c.appID, c.secret); err == nil {
			t.Errorf("应拒绝无效的应用标识或密钥")
		}
	}
}

func TestSignedClientStopsRedirectsAndRejectsConnect(t *testing.T) {
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected request to redirect destination")
	}))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		http.Redirect(w, r, dst.URL, http.StatusTemporaryRedirect)
	}))
	defer src.Close()
	client, err := NewClient("profiles", "app", strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Request(t.Context()).SetBody("test").Post(src.URL)
	if err != nil || resp.StatusCode() != http.StatusTemporaryRedirect {
		t.Fatalf("redirect response=%v err=%v", resp, err)
	}
	if _, err := client.Request(t.Context()).Execute(http.MethodConnect, src.URL); err == nil {
		t.Error("unexpected signed CONNECT")
	}
}
