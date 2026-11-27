// Package appsigv1 定义 Go 调用方和验签方共用的 v1 协议字节约定。
package appsigv1

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/bpcoder16/pixiu/cryptox"
)

const (
	AppIDHeader     = "X-App-Id"
	TimestampHeader = "X-Timestamp"
	NonceHeader     = "X-Nonce"
	SignatureHeader = "X-Signature"
)

var appIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// NewMAC 验证凭证，解码部署用的十六进制密钥。
func NewMAC(appID, appSecret string) (*cryptox.HMACSHA256, error) {
	if !appIDPattern.MatchString(appID) {
		return nil, errors.New("appsign: invalid AppID")
	}
	if len(appSecret) != 2*sha256.Size {
		return nil, errors.New("appsign: AppSecret must encode 32 bytes in hex")
	}
	key, err := hex.DecodeString(appSecret)
	if err != nil {
		return nil, errors.New("appsign: invalid hex in AppSecret")
	}
	return cryptox.NewHMACSHA256(key)
}

// Message 使用原始请求目标和正文的 SHA-256 摘要构造签名原文。
func Message(appID, timestamp, nonce, method, target string, bodySHA256 []byte) []byte {
	return []byte(strings.Join([]string{appID, timestamp, nonce, method, target, hex.EncodeToString(bodySHA256)}, "\n"))
}
