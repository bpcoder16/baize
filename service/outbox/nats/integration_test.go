package nats

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/baize/service/outbox"
	"github.com/bpcoder16/pixiu/infra/natsx"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"
)

type testPublisher func(context.Context, model.Delivery) error

func (f testPublisher) Publish(ctx context.Context, d model.Delivery) error { return f(ctx, d) }

func TestDatabaseToJetStreamRecovery(t *testing.T) {
	for backend, key := range map[constant.Backend]string{
		constant.MySQL:      "BAIZE_OUTBOX_MYSQL_DSN",
		constant.PostgreSQL: "BAIZE_OUTBOX_POSTGRESQL_DSN",
	} {
		t.Run(string(backend), func(t *testing.T) {
			dsn := os.Getenv(key)
			if dsn == "" {
				t.Skip("未设置 " + key + "；端到端数据库测试未运行")
			}
			if !isolatedNamedClients(t) {
				return
			}
			db := testdb.Open(t, backend)
			s, err := server.NewServer(&server.Options{
				Host:      "127.0.0.1",
				Port:      -1,
				JetStream: true,
				StoreDir:  t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			t.Cleanup(func() {
				s.Shutdown()
				s.WaitForShutdown()
			})
			if !s.ReadyForConnections(5 * time.Second) {
				t.Fatal("NATS 未就绪")
			}
			client, err := natsx.NewNamed(natsx.Config{
				Name: "events",
				URLs: []string{s.ClientURL()},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = natsx.CloseAll() })
			ctx := context.Background()
			stream, err := client.JetStream().CreateStream(ctx, jetstream.StreamConfig{
				Name:     "EVENTS",
				Subjects: []string{"events.created"},
				Storage:  jetstream.FileStorage,
			})
			if err != nil {
				t.Fatal(err)
			}
			store, err := repo.Open(ctx, repo.StoreConfig{
				Backend:  backend,
				MasterDB: db,
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewJetStreamHandler(map[string]*natsx.Client{"events": client})
			sendCtx, cancelSend := context.WithCancel(ctx)
			defer cancelSend()
			var calls atomic.Int64
			publish := func(ctx context.Context, d model.Delivery) error {
				err := handler.Publish(ctx, d)
				if err == nil && calls.Add(1) == 1 {
					// Broker 已确认，调用方却在事务回写前取消。补偿必须沿用同一个消息 ID。
					cancelSend()
				}
				return err
			}
			worker, err := outbox.NewWorker(store, constant.NatsJetStream, outbox.HandlerConfig{
				Handler:       testPublisher(publish),
				ResourceNames: []string{"events"},
			})
			if err != nil {
				t.Fatal(err)
			}
			unregister, err := outbox.Register(worker)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unregister() })
			message := outbox.JetStreamMessage{
				Header:       map[string][]string{},
				ExpiresAt:    time.Now().Add(time.Hour),
				ResourceName: "events",
				Subject:      "events.created",
				Data:         []byte("original"),
			}
			rollback := errors.New("业务回滚")
			var id int64
			err = db.Transaction(func(tx *gorm.DB) error {
				var err error
				id, err = outbox.EnqueueJetStreamTx(tx, message)
				if err != nil {
					return err
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if err := worker.Send(ctx, id); !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("回滚任务应返回 ErrNotFound: %v", err)
			}
			if calls.Load() != 0 {
				t.Fatal("未提交任务不能发送")
			}
			if err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				id, err = outbox.EnqueueJetStreamTx(tx, message)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			message.Data[0] = 'X'
			if err := worker.Send(sendCtx, id); err == nil {
				t.Fatal("发送事务已取消，不能报告回写成功")
			}
			var record model.Record
			// 等待回滚释放行锁，确认任务仍可补偿。
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return tx.Raw("SELECT * FROM baize_outbox WHERE id = ? FOR UPDATE", id).Scan(&record).Error
			})
			if err != nil || record.Status != constant.OutboxPending || record.Attempts != 0 {
				t.Fatalf("收到确认后取消应保留待发记录: %+v %v", record, err)
			}
			if err := worker.RetryPending(ctx, 10); err != nil {
				t.Fatal(err)
			}
			if err := db.WithContext(ctx).Table("baize_outbox").Where("id = ?", id).Take(&record).Error; err != nil ||
				record.ResourceName != "events" || record.Status != constant.OutboxSucceeded || record.Attempts != 1 {
				t.Fatalf("补偿状态错误: %+v %v", record, err)
			}

			info, err := stream.Info(ctx)
			if err != nil || info.State.Msgs != 1 || calls.Load() != 2 {
				t.Fatalf("补偿未去重: %+v %v, calls=%d", info, err, calls.Load())
			}
			stored, err := stream.GetMsg(ctx, 1)
			if err != nil || string(stored.Data) != "original" || stored.Header.Get("Nats-Msg-Id") != strconv.FormatInt(id, 10) {
				t.Fatalf("入表快照不稳定: %+v %v", stored, err)
			}
		})
	}
}
