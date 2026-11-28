// Package appsign 为 Pixiu httpcall 客户端的出站请求生成 AppID / AppSecret 签名。
package appsign

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/bpcoder16/baize/service/appauth/internal/appsigv1"
	"github.com/bpcoder16/pixiu/cryptox"
	"github.com/bpcoder16/pixiu/infra/httpcall"
	"github.com/go-resty/resty/v2"
)

// NewClient 为一个下游创建可并发复用的签名客户端。凭证在初始化时校验。
// 默认透传当前请求的 logId，可通过 httpcall.OptPropagateLogID(false) 关闭。
// 签名占用 Resty 的 PreRequestHook，并将重定向响应交回调用方。
func NewClient(name, appID, appSecret string, opts ...httpcall.Option) (*httpcall.Client, error) {
	mac, err := appsigv1.NewMAC(appID, appSecret)
	if err != nil {
		return nil, err
	}
	sign := httpcall.OptResty(
		func(c *resty.Client) {
			c.GetClient().CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			c.SetPreRequestHook(
				func(_ *resty.Client, req *http.Request) error {
					return signRequest(req, appID, mac)
				},
			)
		},
	)
	opts = slices.Concat([]httpcall.Option{httpcall.OptPropagateLogID(true)}, opts, []httpcall.Option{sign})
	return httpcall.New(name, opts...), nil
}

func signRequest(req *http.Request, appID string, mac *cryptox.HMACSHA256) error {
	if req.Method == http.MethodConnect {
		return errors.New("appsign: CONNECT tunnels use an authority target")
	}
	if req.URL.Host == "" || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
		return errors.New("appsign: expected an absolute HTTP or HTTPS URL")
	}

	// 从 Resty 已序列化的正文副本流式计算摘要，保留待发送正文的读取位置。
	bodyHash := sha256.New()
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return errors.New("appsign: request body cannot be replayed for signing")
		}
		body, err := req.GetBody()
		if err != nil {
			return err
		}
		defer func() { _ = body.Close() }()
		if _, err := io.Copy(bodyHash, body); err != nil {
			return err
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	nonce := hex.EncodeToString(random[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature, err := mac.Sign(appsigv1.Message(appID, timestamp, nonce, req.Method, req.URL.RequestURI(), bodyHash.Sum(nil)))
	if err != nil {
		return err
	}
	req.Header.Set(appsigv1.AppIDHeader, appID)
	req.Header.Set(appsigv1.TimestampHeader, timestamp)
	req.Header.Set(appsigv1.NonceHeader, nonce)
	req.Header.Set(appsigv1.SignatureHeader, hex.EncodeToString(signature))
	return nil
}
