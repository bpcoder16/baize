package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	outbox "github.com/bpcoder16/baize/service/outbox"
)

func TestStoreLocationConfiguration(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	local := time.Local
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		for _, tc := range []struct {
			name     string
			location *time.Location
		}{
			{name: "默认UTC"},
			{name: "北京时间", location: shanghai},
			{name: "自定义偏移", location: time.FixedZone("custom +05:45", 5*3600+45*60)},
			{name: "夏令时", location: newYork},
		} {
			t.Run(string(backend)+"/"+tc.name, func(t *testing.T) {
				db := testdb.Open(t, backend)
				store, err := repo.Open(t.Context(), repo.StoreConfig{
					Backend:  backend,
					MasterDB: db,
					Location: tc.location,
				})
				if err != nil {
					t.Fatal(err)
				}
				want := tc.location
				if want == nil {
					want = time.UTC
				}
				if store.TestConfig().Location != want {
					t.Fatalf("配置时区未保留: %v, want %v", store.TestConfig().Location, want)
				}
			})
		}
	}
	if time.Local != local {
		t.Fatal("Outbox 不应修改进程时区")
	}
}

// 配置时区必须覆盖创建时间、过期时间往返与实际到期判定。
func TestConfiguredLocationLifecycle(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	newYork, _ := time.LoadLocation("America/New_York")
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		for _, location := range []*time.Location{time.UTC, shanghai, time.FixedZone("custom +05:45", 20700), newYork} {
			t.Run(string(backend)+"/"+location.String(), func(t *testing.T) {
				base, db := liveStore(t, backend)
				cfg := *base.TestConfig()
				cfg.Location = location
				s, err := repo.Open(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
				value := task()
				value.ExpiresAt = expiry
				id := enqueueTask(t, s, db, value)
				r, err := s.TestReadRecord(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if r.ExpiresAt == nil || !r.ExpiresAt.Equal(expiry) || time.Since(r.CreatedAt).Abs() > 10*time.Second {
					t.Fatalf("时刻偏移: %+v", r)
				}
				if !r.CreatedAt.Equal(r.UpdatedAt) {
					t.Fatalf("同一次入队的创建时间和更新时间应一致: created_at=%v updated_at=%v", r.CreatedAt, r.UpdatedAt)
				}
				for _, v := range []time.Time{r.CreatedAt, r.UpdatedAt, *r.ExpiresAt} {
					if v.Location() != location {
						t.Fatalf("未使用配置时区: %v", v)
					}
				}
				w := testWorker(t, s, func(context.Context, model.Delivery) error { return nil })
				if err := w.Send(ctx, id); err != nil {
					t.Fatal(err)
				}
				r, err = s.TestReadRecord(ctx, id)
				if err != nil || r.Status != constant.OutboxSucceeded || r.FinishedAt == nil || r.FinishedAt.Location() != location {
					t.Fatalf("完成时间错误: %+v err=%v", r, err)
				}
				if !r.FinishedAt.Equal(r.UpdatedAt) {
					t.Fatalf("同一次回写的完成时间和更新时间应一致: finished_at=%v updated_at=%v", r.FinishedAt, r.UpdatedAt)
				}
				value = task()
				value.ExpiresAt = expiry.Add(-2 * time.Hour)
				id = enqueueTask(t, s, db, value)
				if err := w.Send(ctx, id); !errors.Is(err, outbox.ErrExpired) {
					t.Fatalf("过期过滤错误: %v", err)
				}
			})
		}
	}
}
