// Package ossboot 在共享实例就绪后准备上传表并发布 OSS 上传服务。
package ossboot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/internal/db"
	"github.com/bpcoder16/baize/bootstrap/internal/migration"
	"github.com/bpcoder16/baize/constant"
	repo "github.com/bpcoder16/baize/repository/oss"
	"github.com/bpcoder16/baize/service/oss"
	"github.com/bpcoder16/pixiu/infra/aliyun/ossx"
	"github.com/bpcoder16/pixiu/infra/aliyun/stsx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

// Config 引用 Plan 中的命名资源。两个 OSS 实例名相同表示只有公有桶。
type Config struct {
	Backend           constant.Backend
	DatabaseName      string
	STSName           string
	PublicBucketName  string
	PrivateBucketName string
}

// Prepare 读取 baize.oss.yaml，按选定数据库的迁移开关准备上传表并发布服务。
func Prepare(cfg Config) bootstrap.Step {
	return bootstrap.Step{
		Name: "baize.oss-upload",
		Init: func(ctx context.Context, dir string, stack *lifecycle.Stack) error {
			if cfg.Backend != constant.MySQL && cfg.Backend != constant.PostgreSQL {
				return fmt.Errorf("ossboot: unsupported database backend %q", cfg.Backend)
			}
			database, err := db.LookupClient(ctx, cfg.Backend, cfg.DatabaseName)
			if err != nil {
				return err
			}
			sts, err := bootstrap.LookupDeclared(ctx, constant.ResourceAliyunSTS, cfg.STSName, stsx.Named)
			if err != nil {
				return err
			}
			public, err := bootstrap.LookupDeclared(ctx, constant.ResourceAliyunOSS, cfg.PublicBucketName, ossx.Named)
			if err != nil {
				return err
			}
			clients := oss.BucketClients{Public: public}
			if cfg.PrivateBucketName != cfg.PublicBucketName {
				private, lookupErr := bootstrap.LookupDeclared(ctx, constant.ResourceAliyunOSS, cfg.PrivateBucketName, ossx.Named)
				if lookupErr != nil {
					return lookupErr
				}
				clients.Private = private
			}
			settings, err := oss.LoadConfig(filepath.Join(dir, "baize.oss.yaml"))
			if err != nil {
				return err
			}
			buckets, err := oss.BindBuckets(settings, clients, sts)
			if err != nil {
				return err
			}
			policy, err := migration.LoadConfig(dir, string(cfg.Backend), cfg.DatabaseName)
			if err != nil {
				return err
			}
			schemaCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
			defer cancel()
			openStore := repo.OpenExisting
			if policy.Enabled {
				openStore = repo.Open
			}
			store, err := openStore(schemaCtx, repo.StoreConfig{
				Backend: cfg.Backend,
				Client:  database,
			})
			if err != nil {
				return fmt.Errorf("ossboot: prepare upload table on %q: %w", cfg.DatabaseName, err)
			}
			uploads, err := oss.NewUploads(store, buckets)
			if err != nil {
				return err
			}
			release, err := oss.Register(uploads)
			if err != nil {
				return err
			}
			if registerErr := stack.Register(release); registerErr != nil {
				return errors.Join(registerErr, release())
			}
			return nil
		},
	}
}
