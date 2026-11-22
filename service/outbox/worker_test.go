package outbox_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/baize/service/outbox"
	"gorm.io/gorm"
)

func TestWorkersSendAndRetryOnlyTheirBoundType(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			db := testdb.Open(t, backend)
			s, err := repo.Open(ctx, repo.StoreConfig{
				Backend:  backend,
				MasterDB: db,
			})
			if err != nil {
				t.Fatal(err)
			}
			const other constant.OutboxHandlerType = "test_other"
			var first, second int64
			if err := db.Transaction(func(tx *gorm.DB) error {
				task := model.Task{
					HandlerType:  constant.NatsJetStream,
					ResourceName: "events",
					Payload:      []byte("message"),
				}
				var insertErr error
				first, insertErr = s.InsertTx(tx, task)
				if insertErr != nil {
					return insertErr
				}
				task.HandlerType = other
				second, insertErr = s.InsertTx(tx, task)
				return insertErr
			}); err != nil {
				t.Fatal(err)
			}

			var published []int64
			jetStream := newWorker(t, s, func(_ context.Context, d model.Delivery) error {
				published = append(published, d.ID)
				return nil
			})
			secondWorker, err := outbox.NewWorker(s, other, testPublisher(func(_ context.Context, d model.Delivery) error {
				published = append(published, d.ID)
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			for _, worker := range []*outbox.Worker{jetStream, secondWorker} {
				unregister, err := outbox.Register(worker)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = unregister() })
			}
			if err := outbox.SendJetStream(ctx, second); !errors.Is(err, outbox.ErrHandlerTypeMismatch) {
				t.Fatalf("传错类型应返回错误并保留任务: %v", err)
			}
			if err := jetStream.RetryPending(ctx, 1); err != nil {
				t.Fatal(err)
			}
			var untouched model.Record
			err = db.Table("baize_outbox").Where("id = ?", second).Take(&untouched).Error
			if err != nil || untouched.Status != constant.OutboxPending || untouched.Attempts != 0 || untouched.LastError != "" {
				t.Fatalf("其他类型的任务被改动: %+v, %v", untouched, err)
			}
			if err := outbox.Send(ctx, other, second); err != nil {
				t.Fatal(err)
			}
			if err := outbox.SendJetStream(ctx, second); !errors.Is(err, outbox.ErrHandlerTypeMismatch) {
				t.Fatalf("已成功的其他类型任务也应返回类型不匹配: %v", err)
			}
			if err := outbox.Send(ctx, other, second); err != nil {
				t.Fatalf("相同类型重复发送已成功任务应返回 nil: %v", err)
			}
			if !slices.Equal(published, []int64{first, second}) {
				t.Fatalf("投递顺序错误: %v", published)
			}
		})
	}
}
