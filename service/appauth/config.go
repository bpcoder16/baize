// Package appauth 验证入站应用签名、路由权限及 Redis 防重放记录。
package appauth

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/bpcoder16/baize/service/appauth/internal/appsigv1"
	"github.com/bpcoder16/pixiu/cryptox"
	"github.com/bpcoder16/pixiu/infra/configx"
	"github.com/redis/go-redis/v9"
)

const defaultMaxBodyBytes = 1 << 20

type config struct {
	MaxBodyBytes int64 `mapstructure:"maxBodyBytes"`
	Clients      []struct {
		AppID       string   `mapstructure:"appID"`
		AppSecret   string   `mapstructure:"appSecret"`
		Permissions []string `mapstructure:"permissions"`
	} `mapstructure:"clients"`
}

type client struct {
	mac         *cryptox.HMACSHA256
	permissions map[string]bool
}

type verifier struct {
	db           *redis.Client
	keyPrefix    string
	clients      map[string]client
	maxBodyBytes int64
}

var active atomic.Pointer[verifier]

// Load 从配置文件装配进程级验证器。db 由宿主管理，appName 用作 Redis 防重放命名空间。
// 整批验证成功后才发布；返回的释放函数只撤销本次发布。
func Load(file, appName string, db *redis.Client) (func() error, error) {
	if db == nil || strings.TrimSpace(appName) == "" {
		return nil, errors.New("appauth: Redis client and app name required")
	}
	// go-redis 将 -1 归一化为 0。SET NX 成功后若应答丢失，重试会把本次请求误判为重放。
	if db.Options().MaxRetries != 0 {
		return nil, errors.New("appauth: disable Redis command retries with maxRetries: -1")
	}
	cfg, err := configx.Parse[config](file)
	if err != nil {
		return nil, err
	}
	if cfg.MaxBodyBytes < 0 || cfg.Clients == nil {
		return nil, errors.New("appauth: clients list required and maxBodyBytes must be nonnegative")
	}
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	v := &verifier{
		db:           db,
		keyPrefix:    "baize:appauth:v1:" + appName + ":",
		clients:      make(map[string]client, len(cfg.Clients)),
		maxBodyBytes: cfg.MaxBodyBytes,
	}
	for _, spec := range cfg.Clients {
		if _, exists := v.clients[spec.AppID]; exists {
			return nil, fmt.Errorf("appauth: duplicate AppID %q", spec.AppID)
		}
		mac, macErr := appsigv1.NewMAC(spec.AppID, spec.AppSecret)
		if macErr != nil {
			return nil, fmt.Errorf("appauth: AppID %q: %w", spec.AppID, macErr)
		}
		perms := make(map[string]bool, len(spec.Permissions))
		for _, perm := range spec.Permissions {
			if perm == "" || strings.TrimSpace(perm) != perm || perms[perm] {
				return nil, fmt.Errorf("appauth: invalid or duplicate permission for AppID %q", spec.AppID)
			}
			perms[perm] = true
		}
		v.clients[spec.AppID] = client{mac: mac, permissions: perms}
	}
	if !active.CompareAndSwap(nil, v) {
		return nil, errors.New("appauth: verifier already active")
	}
	return func() error {
		active.CompareAndSwap(v, nil)
		return nil
	}, nil
}
