package oss

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bpcoder16/pixiu/infra/configx"
)

type Scene struct {
	BucketType    string   `mapstructure:"bucketType"`
	TargetDir     string   `mapstructure:"targetDir"`
	MaxBatchCount int      `mapstructure:"maxBatchCount"`
	MaxSize       int64    `mapstructure:"maxSize"`
	AllowedExts   []string `mapstructure:"allowedExts"`
	// STSDuration 为该场景的 STS 凭据有效期，默认 40 分钟。
	STSDuration time.Duration `mapstructure:"stsDuration"`
}

type Config struct {
	// STSRoleARN 留空时不签发上传凭据。
	STSRoleARN string `mapstructure:"stsRoleARN"`
	// STSSessionName 为角色会话名，留空时使用 oss-upload。
	STSSessionName string `mapstructure:"stsSessionName"`
	// PublicBucketCDN 用于生成公有下载地址，允许包含路径前缀。
	PublicBucketCDN string `mapstructure:"publicBucketCDN"`
	// PrivateBucketURLExpires 指定私有下载 URL 的签名有效期，默认 15 分钟。
	PrivateBucketURLExpires *time.Duration `mapstructure:"privateBucketURLExpires"`
	// configx 将映射键转为小写，场景查询须同步归一化。
	Scenes map[string]Scene `mapstructure:"scenes"`
}

// LoadConfig 严格读取并校验配置，结果只属于本次调用。
func LoadConfig(file string) (*Config, error) {
	cfg, err := configx.Parse[Config](file)
	if err != nil {
		return nil, err
	}
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

var extensionPattern = regexp.MustCompile(`^\.[a-z0-9]{1,19}$`)

func (cfg *Config) validate() error {
	if cfg.PublicBucketCDN != "" {
		cdn, err := url.Parse(cfg.PublicBucketCDN)
		if err != nil || strings.TrimSpace(cfg.PublicBucketCDN) != cfg.PublicBucketCDN ||
			(cdn.Scheme != "http" && cdn.Scheme != "https") || cdn.Hostname() == "" ||
			cdn.User != nil || cdn.RawQuery != "" || cdn.ForceQuery || cdn.Fragment != "" {
			return fmt.Errorf("invalid OSS publicBucketCDN: must be an HTTP(S) URL without credentials, query or fragment")
		}
		cfg.PublicBucketCDN = cdn.String()
	}
	if cfg.STSSessionName == "" {
		cfg.STSSessionName = "oss-upload"
	}
	if cfg.PrivateBucketURLExpires == nil {
		cfg.PrivateBucketURLExpires = new(15 * time.Minute)
	}
	if ttl := *cfg.PrivateBucketURLExpires; ttl < time.Second || ttl > 7*24*time.Hour || ttl%time.Second != 0 {
		return fmt.Errorf("invalid OSS privateBucketURLExpires: must be whole seconds between 1s and 168h")
	}
	if len(cfg.Scenes) == 0 {
		return fmt.Errorf("OSS upload scenes are required")
	}
	scenes := make(map[string]Scene, len(cfg.Scenes))
	for name, scene := range cfg.Scenes {
		if !validSceneName(name) {
			return fmt.Errorf("invalid OSS upload scene %q: name must contain 1 to 64 bytes without leading or trailing whitespace", name)
		}
		if scene.BucketType != "public" && scene.BucketType != "private" {
			return fmt.Errorf("invalid OSS upload scene %q: bucketType must be \"public\" or \"private\"", name)
		}
		if scene.TargetDir == "" || len(scene.TargetDir) > 400 {
			return fmt.Errorf("invalid OSS upload scene %q: targetDir must contain 1 to 400 bytes", name)
		}
		if scene.TargetDir == "." || scene.TargetDir == ".." ||
			path.Clean(scene.TargetDir) != scene.TargetDir || strings.HasPrefix(scene.TargetDir, "/") ||
			strings.HasPrefix(scene.TargetDir, "../") {
			return fmt.Errorf("invalid OSS upload scene %q: targetDir must be a clean relative path without \".\" or \"..\" segments", name)
		}
		// 目录会写入 STS 策略，禁止通配符扩大授权范围。
		if strings.ContainsAny(scene.TargetDir, "\\*?\r\n") {
			return fmt.Errorf("invalid OSS upload scene %q: targetDir must not contain backslashes, wildcards, or line breaks", name)
		}
		if scene.MaxBatchCount < 1 || scene.MaxBatchCount > 100 {
			return fmt.Errorf("invalid OSS upload scene %q: maxBatchCount must be between 1 and 100", name)
		}
		if scene.MaxSize < 1 || scene.MaxSize > 1<<30 {
			return fmt.Errorf("invalid OSS upload scene %q: maxSize must be between 1 and 1073741824 bytes", name)
		}
		if scene.STSDuration == 0 {
			scene.STSDuration = 40 * time.Minute
		}
		if scene.STSDuration < 15*time.Minute || scene.STSDuration > 12*time.Hour || scene.STSDuration%time.Second != 0 {
			return fmt.Errorf("invalid OSS upload scene %q: stsDuration must be whole seconds between 15m and 12h", name)
		}
		if len(scene.AllowedExts) == 0 {
			return fmt.Errorf("invalid OSS upload scene %q: allowedExts must not be empty", name)
		}
		for _, ext := range scene.AllowedExts {
			if !extensionPattern.MatchString(ext) {
				return fmt.Errorf("invalid OSS upload scene %q: allowedExts entry %q must be a dot followed by 1 to 19 lowercase letters or digits", name, ext)
			}
		}
		// 场景名统一按小写查找。
		key := strings.ToLower(name)
		if _, exists := scenes[key]; exists {
			return fmt.Errorf("duplicate OSS upload scene %q", name)
		}
		scenes[key] = scene
	}
	cfg.Scenes = scenes
	return nil
}

func validSceneName(name string) bool {
	return name != "" && len(name) <= 64 && strings.TrimSpace(name) == name
}

// MaxDirectUploadSize 用于在 multipart 落盘前限制整个请求的体积。
func (cfg *Config) MaxDirectUploadSize() int64 {
	var size int64
	for _, scene := range cfg.Scenes {
		if scene.MaxSize > size {
			size = scene.MaxSize
		}
	}
	return size
}
