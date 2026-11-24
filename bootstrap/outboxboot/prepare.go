// Package outboxboot 在 Prepare 阶段装配 JetStream 事务发件箱。
package outboxboot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bpcoder16/baize/bootstrap"
	"github.com/bpcoder16/baize/bootstrap/cronboot"
	"github.com/bpcoder16/baize/bootstrap/internal/db"
	"github.com/bpcoder16/baize/bootstrap/internal/migration"
	"github.com/bpcoder16/baize/constant"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/baize/service/outbox"
	outboxnats "github.com/bpcoder16/baize/service/outbox/nats"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/infra/env"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/bpcoder16/pixiu/lifecycle"
	"github.com/bpcoder16/pixiu/logit"
)

// JetStreamConfig 选择业务数据库实例、按优先顺序排列的发布实例，以及补偿任务的调度预算。
type JetStreamConfig struct {
	Backend         constant.Backend
	DatabaseName    string
	ResourceNames   []string
	CronTaskEvery   time.Duration
	CronTaskTimeout time.Duration
}

// PrepareJetStream 在共享资源就绪后装配默认 Outbox，首个资源是入队的默认目标。
// 数据库和名单中的 NATS 实例须在本次 Plan.Resources 中声明。
func PrepareJetStream(cfg JetStreamConfig) bootstrap.Step {
	names := slices.Clone(cfg.ResourceNames)
	return bootstrap.Step{
		Name: "baize.jetstream-outbox",
		Init: func(ctx context.Context, dir string, stack *lifecycle.Stack) error {
			if (cfg.Backend != constant.MySQL && cfg.Backend != constant.PostgreSQL) || !bootstrap.ValidNamePart(cfg.DatabaseName) {
				return fmt.Errorf("invalid outbox database %q/%q", cfg.Backend, cfg.DatabaseName)
			}
			if len(names) == 0 {
				return errors.New("outbox requires at least one JetStream resource name")
			}
			if cfg.CronTaskEvery < 100*time.Millisecond || cfg.CronTaskTimeout <= 0 {
				return errors.New("outbox requires CronTaskEvery >= 100ms and CronTaskTimeout > 0")
			}
			migrationCfg, err := migration.LoadConfig(dir, string(cfg.Backend), cfg.DatabaseName)
			if err != nil {
				return err
			}
			clients, err := jetStreamClients(ctx, names)
			if err != nil {
				return err
			}
			handler, err := outboxnats.NewJetStreamHandler(names[0], clients)
			if err != nil {
				return err
			}
			database, err := db.LookupClient(ctx, cfg.Backend, cfg.DatabaseName)
			if err != nil {
				return err
			}
			schemaCtx, cancel := context.WithTimeout(ctx, migrationCfg.Timeout)
			defer cancel()
			openStore := repo.OpenExisting
			if migrationCfg.Enabled {
				openStore = repo.Open
			}
			store, err := openStore(schemaCtx, repo.StoreConfig{
				Backend:  cfg.Backend,
				Client:   database,
				Location: env.TimeLocation(),
			})
			if err != nil {
				return fmt.Errorf("outbox schema %s/%s: %w", cfg.Backend, cfg.DatabaseName, err)
			}
			worker, err := outbox.NewWorker(store, constant.NatsJetStream, handler)
			if err != nil {
				return err
			}
			unregister, err := outbox.Register(worker)
			if err != nil {
				return err
			}
			if err = stack.Register(unregister); err != nil {
				return errors.Join(err, unregister())
			}
			// 最后登记任务。重名或 Cron 未启用时撤销本次绑定，栈内的回收可重复调用。
			if err = cronboot.AddJob(ctx, retryJob(cfg, worker)); err != nil {
				return errors.Join(err, unregister())
			}
			return nil
		},
	}
}

func retryJob(cfg JetStreamConfig, worker *outbox.Worker) cronx.Job {
	return cronx.Job{
		Name:         "baize.outbox.retry." + string(constant.NatsJetStream),
		Every:        cfg.CronTaskEvery,
		Timeout:      cfg.CronTaskTimeout,
		SuccessLevel: logit.InfoLevel,
		Handler:      func(ctx context.Context) error { return worker.RetryPending(ctx, 200) },
	}
}

// 逐个确认白名单实例属于本次 Plan，且当前账号可用 JetStream。
func jetStreamClients(ctx context.Context, names []string) (map[string]*natsx.Client, error) {
	clients := make(map[string]*natsx.Client, len(names))
	for _, name := range names {
		if len(name) > 64 {
			return nil, fmt.Errorf("outbox resource name exceeds 64 bytes: %q", name)
		}
		client, err := bootstrap.LookupDeclared(ctx, constant.ResourceNATS, name, natsx.Named)
		if err != nil {
			return nil, err
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = client.JetStream().AccountInfo(probe)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("probe JetStream %q: %w", name, err)
		}
		clients[name] = client
	}
	return clients, nil
}
