package share

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bpcoder16/pixiu/cryptox"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

var (
	ErrUnavailable    = errors.New("share: service unavailable")
	ErrUnregistered   = errors.New("share: page or request type not registered")
	ErrInvalidRequest = errors.New("share: invalid owner or request")
	ErrMissingPageURL = errors.New("share: pageURL is required to build a URL")
	ErrURLTooLong     = errors.New("share: URL too long")
	ErrInvalidShare   = errors.New("share: invalid link")
)

const (
	maxURLBytes   = 2048
	protocol      = "s1"
	purposePrefix = "baize.share/" + protocol + "/"
)

type envelope struct {
	CreatedAt   *int64          `json:"t"`
	OwnerID     string          `json:"o"`
	HandlerName string          `json:"p"`
	Request     json.RawMessage `json:"r"`
}

// 发布后只读，单次签发或打开始终使用同一份注册表和密钥配置。
type service struct {
	pageURL     *url.URL
	activeKeyID string
	keys        map[string]*cryptox.AESGCM
	pages       map[string]Registration
	validator   binding.StructValidator
}

var active atomic.Pointer[service]

func validOwner(owner string) bool {
	return len(owner) > 0 && len(owner) <= 128 && utf8.ValidString(owner) &&
		strings.TrimSpace(owner) == owner && strings.IndexFunc(owner, unicode.IsControl) < 0
}

func (s *service) encryptToken(name, owner string, request json.RawMessage) (string, error) {
	now := time.Now().Unix()
	payload, err := json.Marshal(envelope{
		CreatedAt:   &now,
		OwnerID:     owner,
		HandlerName: name,
		Request:     request,
	})
	if err != nil {
		return "", ErrInvalidRequest
	}
	if len(payload) > maxURLBytes {
		return "", ErrURLTooLong
	}
	id := s.activeKeyID
	ciphertext, err := s.keys[id].EncryptToken(payload, purposePrefix+id)
	if err != nil {
		return "", err
	}
	token := protocol + "." + id + "." + ciphertext
	if len(token) > maxURLBytes {
		return "", ErrURLTooLong
	}
	return token, nil
}

// Open 在当前 Gin 请求中同步调用指定页面的 handler。分发前失败仅返回错误，
// 成功进入 handler 后由宿主负责响应、权限判断和 panic recovery。
func Open(c *gin.Context, token string) error {
	s := active.Load()
	if s == nil {
		return ErrUnavailable
	}
	if c == nil || len(token) > maxURLBytes {
		return ErrInvalidShare
	}
	rest, ok := strings.CutPrefix(token, protocol+".")
	if !ok {
		return ErrInvalidShare
	}
	id, ciphertext, ok := strings.Cut(rest, ".")
	box := s.keys[id]
	if !ok || box == nil {
		return ErrInvalidShare
	}
	// kid 和协议版本参与认证，认证完成前不使用任何业务字段。
	plain, err := box.DecryptToken(ciphertext, purposePrefix+id)
	if err != nil {
		return ErrInvalidShare
	}
	var payload envelope
	if err = json.Unmarshal(plain, &payload); err != nil || payload.CreatedAt == nil || !validOwner(payload.OwnerID) {
		return ErrInvalidShare
	}
	page, ok := s.pages[payload.HandlerName]
	if !ok {
		return ErrInvalidShare
	}
	return page.dispatch(c, payload.Request, s.validator)
}
