package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/pixiu/logit"
)

// Worker 执行主动发送与一轮补偿；支持同步 Send 和独立 5 秒超时的 SendAsync。
// 每条任务使用独立事务行锁；多实例补偿轮次由应用的 cron 分布式锁协调。
type Worker struct {
	store       *repo.Store
	handlerType constant.OutboxHandlerType
	binding     HandlerConfig
	retryCursor atomic.Pointer[repo.PendingCursor]
}

// NewWorker 为任务表绑定一种发布类型，发布器和目标清单在构造后固定。
func NewWorker(store *repo.Store, handlerType constant.OutboxHandlerType, cfg HandlerConfig) (*Worker, error) {
	if store == nil {
		return nil, ErrInvalidArgument
	}
	if !handlerType.Valid() {
		return nil, fmt.Errorf("%w: invalid handler type %q", ErrInvalidArgument, handlerType)
	}
	if cfg.Handler == nil || len(cfg.ResourceNames) == 0 {
		return nil, fmt.Errorf("%w: handler and resource names required", ErrInvalidArgument)
	}
	cfg.ResourceNames = slices.Clone(cfg.ResourceNames)
	return &Worker{
		store:       store,
		handlerType: handlerType,
		binding:     cfg,
	}, nil
}

// Send 在业务事务提交后尝试发送一次。发布失败的原因随投递结果提交，pending 任务由 cron 补偿。
// 任务类型必须与 Worker 绑定的类型一致，否则返回 ErrHandlerTypeMismatch。
// 已成功任务直接返回 nil；过期返回 ErrExpired；同一任务被锁定时返回 ErrBusy。
func (w *Worker) Send(ctx context.Context, id int64) error {
	if ctx == nil || id <= 0 {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	taskErr, storageErr := w.send(ctx, id, nil)
	return errors.Join(taskErr, storageErr)
}

// SendAsync 在最外层业务事务提交后启动 goroutine，用独立的 5 秒超时 context 尝试发送一次。
// 不接收请求 context；Handler 和发送日志通过任务记录中的 logId 关联业务。
// 错误记录日志，pending 任务由 cron 补偿；不提供等待或排空，退出可能中断本次尝试。
// 加锁、发布和回写共用这 5 秒，Handler 须响应取消。
func (w *Worker) SendAsync(id int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.Send(ctx, id); err != nil {
			// Send 的读取或加锁阶段也可能失败，异步调用须保留这些返回错误。
			logit.Error(ctx, "OutboxAsyncFailed", logit.Int64("task_id", id), logit.Str("err", outboxerr.ErrorText(err)))
		}
	}()
}

// RetryPending 只处理 Worker 绑定的类型，按 ID 分页补偿，再于 Store 当地 02:00 至 04:00 分批清理创建超过 30 天的终态任务。
// 每批清理前按 Store 时区检查当前时间；每条补偿尝试使用 5 秒期限，并受调用方取消控制。
// batchSize 为查询页大小。整轮取消后从已尝试 ID 的下一条继续，扫完固定上界后再从头扫描。
// 已锁任务跳过，发布失败或单条超时后继续。其他存储错误或本轮取消结束调用。
// 应挂载到配置分布式锁的 cronx，避免多实例补偿轮次重叠。
func (w *Worker) RetryPending(ctx context.Context, batchSize int) error {
	if ctx == nil || batchSize <= 0 {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var firstErr error
	previous := w.retryCursor.Load()
	var cursor repo.PendingCursor
	if previous != nil {
		cursor = *previous
	}
	scanErr := w.store.ResumePending(ctx, w.handlerType, batchSize, &cursor, func(id int64) error {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		taskErr, storageErr := w.send(attemptCtx, id, &w.handlerType)
		if storageErr != nil {
			// 单条超时可能表现为 GORM 返回期限错误，或 database/sql 已回滚事务。
			timedOut := errors.Is(storageErr, context.DeadlineExceeded) || errors.Is(storageErr, sql.ErrTxDone)
			if !errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || !timedOut {
				return errors.Join(taskErr, storageErr)
			}
			taskErr = context.DeadlineExceeded
		}
		if taskErr != nil && !errors.Is(taskErr, ErrExpired) && firstErr == nil {
			firstErr = fmt.Errorf("outbox: task %d: %w", id, taskErr)
		}
		return nil
	})
	// 失租时两轮可能重叠，各用自己的快照，晚返回的一轮不能覆盖已更新的进度。
	w.retryCursor.CompareAndSwap(previous, &cursor)
	if scanErr != nil {
		return errors.Join(firstErr, scanErr)
	}
	// 空队列以及本轮有单条任务失败时，也要清理历史记录。
	if ctx.Err() == nil {
		if err := w.store.CleanFinished(ctx, w.handlerType, min(batchSize, 500)); err != nil {
			logit.Error(ctx, "OutboxCleanupFailed", logit.Str("err", outboxerr.ErrorText(err)))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(firstErr, err)
	}
	return firstErr
}

// send 让发布与结果回写共用行锁，存储错误单独返回给补偿扫描。
func (w *Worker) send(ctx context.Context, id int64, pendingType *constant.OutboxHandlerType) (error, error) {
	var fields []logit.Field
	taskErr, storageErr := w.store.WithLockedRecord(ctx, id, pendingType, func(row model.Record) (repo.Update, error) {
		// 主动发送按 ID 加锁，终态任务也必须核对类型。
		if row.HandlerType != w.handlerType {
			return repo.Update{}, fmt.Errorf("%w: task %d has type %q, want %q", ErrHandlerTypeMismatch, id, row.HandlerType, w.handlerType)
		}
		switch row.Status {
		case constant.OutboxSucceeded:
			return repo.Update{}, nil
		case constant.OutboxFailed:
			if row.LastError == ErrExpired.Error() {
				return repo.Update{}, ErrExpired
			}
			return repo.Update{}, fmt.Errorf("%w (task %d): %s", ErrInvalidResource, id, row.LastError)
		}
		if row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			return repo.Update{Status: constant.OutboxFailed}, ErrExpired
		}
		ctx = logit.NewContextScope(ctx)
		if row.LogID != "" {
			logit.AddMeta(ctx, logit.Str(logit.LogId, row.LogID))
		}
		begin := time.Now()
		err := publish(ctx, w.binding.Handler, model.Delivery{
			ID:           id,
			ResourceName: row.ResourceName,
			Payload:      row.Payload,
		})
		state := constant.OutboxSucceeded
		if errors.Is(err, ErrInvalidResource) {
			state = constant.OutboxFailed
		} else if err != nil {
			state = constant.OutboxPending
		}
		fields = []logit.Field{
			logit.Int64("task_id", id),
			logit.Str("handler_type", string(row.HandlerType)),
			logit.Int64("attempt", row.Attempts+1),
			logit.Int64("duration_ms", time.Since(begin).Milliseconds()),
			logit.Str("status", string(state)),
		}
		return repo.Update{Status: state, Attempted: true}, err
	})
	if fields != nil {
		if taskErr != nil {
			fields = append(fields, logit.Str("err", outboxerr.ErrorText(taskErr)))
		}
		switch {
		case storageErr != nil:
			logit.Error(ctx, "OutboxWriteFailed", append(fields, logit.Str("write_error", storageErr.Error()))...)
		case taskErr != nil:
			logit.Error(ctx, "OutboxAttempt", fields...)
		default:
			logit.Info(ctx, "OutboxAttempt", fields...)
		}
	}
	return taskErr, storageErr
}
