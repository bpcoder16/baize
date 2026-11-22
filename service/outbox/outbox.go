package outbox

import (
	"context"
	"fmt"
	"sync"

	"github.com/bpcoder16/baize/constant"
	"gorm.io/gorm"
)

var (
	workersMu sync.RWMutex
	workers   = make(map[constant.OutboxHandlerType]*Worker)
)

// Register 按 Worker 绑定的类型登记，每种类型最多登记一个 Worker。
// JetStream 发布器还须实现 ResolveResource(string) (string, error)，供入队时选择目标。
// 返回的清理函数只撤销本次登记，可重复调用。
func Register(w *Worker) (func() error, error) {
	if w == nil || w.store == nil {
		return nil, ErrInvalidArgument
	}
	if w.handlerType == constant.NatsJetStream {
		if _, ok := w.handler.(resourceResolver); !ok {
			return nil, fmt.Errorf("%w: JetStream handler must resolve its publish targets", ErrInvalidArgument)
		}
	}
	workersMu.Lock()
	defer workersMu.Unlock()
	if _, exists := workers[w.handlerType]; exists {
		return nil, fmt.Errorf("outbox: worker already registered for %q", w.handlerType)
	}
	workers[w.handlerType] = w
	// 同一个 Worker 可在撤销后重新登记，旧回收函数只执行一次。
	var once sync.Once
	return func() error {
		once.Do(func() {
			workersMu.Lock()
			defer workersMu.Unlock()
			delete(workers, w.handlerType)
		})
		return nil
	}, nil
}

func workerFor(handlerType constant.OutboxHandlerType) (*Worker, error) {
	workersMu.RLock()
	w := workers[handlerType]
	workersMu.RUnlock()
	if w == nil {
		return nil, fmt.Errorf("%w: %q", ErrHandlerNotRegistered, handlerType)
	}
	return w, nil
}

// EnqueueJetStreamTx 在业务事务中登记消息。ResourceName 省略时使用配置的首个实例。
func EnqueueJetStreamTx(tx *gorm.DB, msg JetStreamMessage) (int64, error) {
	w, err := workerFor(constant.NatsJetStream)
	if err != nil {
		return 0, err
	}
	name, err := w.handler.(resourceResolver).ResolveResource(msg.ResourceName)
	if err != nil {
		return 0, err
	}
	msg.ResourceName = name
	task, err := msg.task()
	if err != nil {
		return 0, err
	}
	return w.store.InsertTx(tx, task)
}

// Send 在最外层事务提交后由指定类型的 Worker 同步尝试一次。
func Send(ctx context.Context, handlerType constant.OutboxHandlerType, id int64) error {
	w, err := workerFor(handlerType)
	if err != nil {
		return err
	}
	return w.Send(ctx, id)
}

// SendAsync 在最外层事务提交后由指定类型的 Worker 异步尝试一次。
// 该类型未登记 Worker 时，在调用方 panic。
func SendAsync(handlerType constant.OutboxHandlerType, id int64) {
	w, err := workerFor(handlerType)
	if err != nil {
		panic(err)
	}
	w.SendAsync(id)
}

// SendJetStream 在最外层事务提交后同步尝试发送一条 JetStream 任务。
func SendJetStream(ctx context.Context, id int64) error {
	return Send(ctx, constant.NatsJetStream, id)
}

// SendAsyncJetStream 在最外层事务提交后异步尝试发送一条 JetStream 任务。
// JetStream Worker 未登记时，在调用方 panic。
func SendAsyncJetStream(id int64) {
	SendAsync(constant.NatsJetStream, id)
}
