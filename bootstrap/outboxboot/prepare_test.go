package outboxboot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/bootstrap/cronboot"
	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/cronx"
	"github.com/bpcoder16/pixiu/lifecycle"
)

func testConfig(names ...string) JetStreamConfig {
	return JetStreamConfig{
		Backend:         constant.MySQL,
		DatabaseName:    "orders",
		ResourceNames:   names,
		CronTaskEvery:   time.Second,
		CronTaskTimeout: time.Minute,
	}
}

func TestPrepareRequiresMigrationAndJetStream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled string
		want    string
	}{
		{"migration-disabled", "false", "migration enabled"},
		{"no-nats", "true", "not declared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "mysql-migrate.orders.yaml"), []byte("enabled: "+tc.enabled), 0600); err != nil {
				t.Fatal(err)
			}
			var stack lifecycle.Stack
			err := PrepareJetStream(testConfig("events")).Init(t.Context(), dir, &stack)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestPrepareRequiresResourceNames(t *testing.T) {
	var stack lifecycle.Stack
	err := PrepareJetStream(testConfig()).Init(t.Context(), t.TempDir(), &stack)
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("empty allowlist: %v", err)
	}
}

func TestRetryJobScheduleAndRegistration(t *testing.T) {
	registry := &cronx.HandlerRegistry{}
	registry.MustRegister(func(context.Context) error { return nil }, "baize.outbox.retry.nats_jetstream")
	ctx, hookErr := cronboot.Service(registry).BeforePrepare(t.Context())
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	cfg := testConfig("events")
	job := retryJob(cfg, nil)
	if job.Every != cfg.CronTaskEvery || job.Timeout != cfg.CronTaskTimeout {
		t.Fatalf("补偿调度参数: %+v", job)
	}
	if err := cronboot.AddJob(t.Context(), job); err == nil || !strings.Contains(err.Error(), "enabled cron") {
		t.Fatalf("必须启用 Cron: %v", err)
	}
	err := cronboot.AddJob(ctx, job)
	if err == nil || err.Error() != `cronboot: duplicate job "baize.outbox.retry.nats_jetstream"` {
		t.Fatalf("宿主与内置任务重名时应报告冲突，得到 %v", err)
	}
}
