package outbox_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/baize/service/outbox"
)

type testPublisher func(context.Context, model.Delivery) error

func (f testPublisher) Publish(ctx context.Context, d model.Delivery) error { return f(ctx, d) }

func newWorker(t *testing.T, store *repo.Store, publish testPublisher) *outbox.Worker {
	t.Helper()
	worker, err := outbox.NewWorker(store, constant.NatsJetStream, outbox.HandlerConfig{
		Handler:       publish,
		ResourceNames: []string{"events"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

// 迁移使用真实连接，发送阶段的 BeginTx 由用例控制。
type beginErrorPool struct {
	*sql.DB
	begin func(context.Context) error
}

func (p beginErrorPool) GetDBConn() (*sql.DB, error) {
	return p.DB, nil
}

func (p beginErrorPool) BeginTx(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, p.begin(ctx)
}

func storeWithBeginError(t *testing.T, begin func(context.Context) error) *repo.Store {
	t.Helper()
	db := testdb.Open(t, constant.MySQL).WithContext(t.Context())
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	db.Statement.ConnPool = beginErrorPool{DB: pool, begin: begin}
	store, err := repo.Open(t.Context(), repo.StoreConfig{
		Backend:  constant.MySQL,
		MasterDB: db,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestDefaultRegistration(t *testing.T) {
	ctx := t.Context()
	checkUnregistered := func() {
		t.Helper()
		if err := outbox.SendJetStream(ctx, 1); !errors.Is(err, outbox.ErrHandlerNotRegistered) {
			t.Errorf("未登记类型的 Send 应返回 ErrHandlerNotRegistered: %v", err)
		}
		if _, err := outbox.EnqueueJetStreamTx(nil, outbox.JetStreamMessage{}); !errors.Is(err, outbox.ErrHandlerNotRegistered) {
			t.Errorf("未登记类型的入队应返回 ErrHandlerNotRegistered: %v", err)
		}
	}
	checkUnregistered()
	t.Run("异步发送要求已登记实例", func(t *testing.T) {
		defer func() {
			if got, _ := recover().(error); !errors.Is(got, outbox.ErrHandlerNotRegistered) {
				t.Errorf("未初始化应明确 panic: %v", got)
			}
		}()
		outbox.SendAsyncJetStream(1)
	})

	// 登记本身只发布 Worker 引用，验证生命周期无需数据库操作。
	worker := newWorker(t, &repo.Store{}, func(context.Context, model.Delivery) error { return nil })
	checkUnregistered()
	unregister, err := outbox.Register(worker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unregister() })
	if _, err := outbox.Register(worker); err == nil {
		t.Fatal("重复登记应返回错误")
	}
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	next, err := outbox.Register(worker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next() })
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	if err := outbox.SendJetStream(ctx, 0); !errors.Is(err, outbox.ErrInvalidArgument) {
		t.Fatalf("旧回收函数撤销了同一 Worker 的新登记: %v", err)
	}
	if err := next(); err != nil {
		t.Fatal(err)
	}
	checkUnregistered()
}

func TestRegistrationIsScopedToHandlerType(t *testing.T) {
	const other constant.OutboxHandlerType = "test_other"
	jetStream := newWorker(t, &repo.Store{}, func(context.Context, model.Delivery) error { return nil })
	second, err := outbox.NewWorker(&repo.Store{}, other, outbox.HandlerConfig{
		Handler:       testPublisher(func(context.Context, model.Delivery) error { return nil }),
		ResourceNames: []string{"other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	removeJetStream, err := outbox.Register(jetStream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeJetStream() })
	removeOther, err := outbox.Register(second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeOther() })
	if err := removeJetStream(); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Send(t.Context(), constant.NatsJetStream, 1); !errors.Is(err, outbox.ErrHandlerNotRegistered) {
		t.Fatalf("撤销后仍能查到 JetStream Worker: %v", err)
	}
	if err := outbox.Send(t.Context(), other, 0); !errors.Is(err, outbox.ErrInvalidArgument) {
		t.Fatalf("撤销 JetStream 后应保留其他类型的 Worker: %v", err)
	}
}

func TestDefaultConcurrentRegistrationAndUse(t *testing.T) {
	type requestKey struct{}
	store := storeWithBeginError(t, func(ctx context.Context) error {
		want, _ := ctx.Value(requestKey{}).(error)
		if want == nil {
			return errors.New("数据库未收到请求 context")
		}
		return want
	})
	worker := newWorker(t, store, func(context.Context, model.Delivery) error { return nil })
	const concurrency = 16
	registered := make(chan func() error, concurrency)
	var group sync.WaitGroup
	for range concurrency {
		group.Go(func() {
			if unregister, err := outbox.Register(worker); err == nil {
				registered <- unregister
			}
		})
	}
	group.Wait()
	close(registered)
	count := len(registered)
	for unregister := range registered {
		t.Cleanup(func() { _ = unregister() })
	}
	if count != 1 {
		t.Fatalf("同一类型只能登记一个 Worker: registrations=%d", count)
	}
	for range concurrency {
		group.Go(func() {
			want := errors.New("本次请求的数据库错误")
			ctx := context.WithValue(t.Context(), requestKey{}, want)
			if err := outbox.Send(ctx, constant.NatsJetStream, 1); !errors.Is(err, want) {
				t.Errorf("发送未绑定该类型的 Store 与本次 context: %v", err)
			}
		})
	}
	group.Wait()
}
