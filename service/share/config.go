package share

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/bpcoder16/pixiu/cryptox"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/gin-gonic/gin/binding"
)

var (
	pageName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	keyID    = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

type config struct {
	PageURL     *string `mapstructure:"pageURL"`
	ActiveKeyID string  `mapstructure:"activeKeyID"`
	Keys        []struct {
		ID  string `mapstructure:"id"`
		Key string `mapstructure:"key"`
	} `mapstructure:"keys"`
}

// Load 供启动装配使用，读取配置并在整批校验成功后发布服务。
// 返回的释放函数可重复调用，只会撤销本次发布。
func Load(file string, pages ...Registration) (func() error, error) {
	cfg, err := configx.Parse[config](file)
	if err != nil {
		return nil, err
	}
	var pageURL *url.URL
	if cfg.PageURL != nil {
		pageURL, err = parsePageURL(*cfg.PageURL)
		if err != nil {
			return nil, err
		}
	}
	keys, err := cfg.loadKeys()
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, errors.New("share: at least one page is required")
	}
	s := &service{
		pageURL:     pageURL,
		keys:        keys,
		activeKeyID: cfg.ActiveKeyID,
		pages:       make(map[string]Registration, len(pages)),
		validator:   binding.Validator,
	}
	if s.validator == nil {
		return nil, errors.New("share: Gin struct validator is required")
	}
	for _, r := range pages {
		if !pageName.MatchString(r.name) || r.dispatch == nil {
			return nil, fmt.Errorf("share: invalid page registration %q", r.name)
		}
		t := r.requestType
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil, fmt.Errorf("share: page %q requires a struct or pointer to a struct", r.name)
		}
		if _, exists := s.pages[r.name]; exists {
			return nil, fmt.Errorf("share: duplicate page %q", r.name)
		}
		s.pages[r.name] = r
	}
	if !active.CompareAndSwap(nil, s) {
		return nil, errors.New("share: service already active")
	}
	return func() error {
		active.CompareAndSwap(s, nil)
		return nil
	}, nil
}

func parsePageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.Contains(raw, "#") {
		return nil, errors.New("share: pageURL requires an absolute HTTPS URL without credentials or fragment")
	}
	if port := u.Port(); port != "" {
		number, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil || number == 0 {
			return nil, errors.New("share: pageURL port must be between 1 and 65535")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || query.Has("s") {
		return nil, errors.New("share: invalid pageURL query or reserved parameter s")
	}
	return u, nil
}

func (cfg *config) loadKeys() (map[string]*cryptox.AESGCM, error) {
	keys := make(map[string]*cryptox.AESGCM, len(cfg.Keys))
	for _, k := range cfg.Keys {
		if !keyID.MatchString(k.ID) || keys[k.ID] != nil {
			return nil, fmt.Errorf("share: invalid or duplicate key id %q", k.ID)
		}
		if len(k.Key) != base64.StdEncoding.EncodedLen(32) {
			return nil, fmt.Errorf("share: key %q requires a base64-encoded 32-byte secret", k.ID)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(k.Key)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("share: invalid AES-256 key for %q", k.ID)
		}
		box, err := cryptox.NewAESGCM(key)
		if err != nil {
			return nil, err
		}
		keys[k.ID] = box
	}
	if keys[cfg.ActiveKeyID] == nil {
		return nil, errors.New("share: activeKeyID must reference a configured key")
	}
	return keys, nil
}
