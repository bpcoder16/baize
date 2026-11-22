package outbox

import (
	"context"
	"errors"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Update 是持锁操作要提交的结果。Status 零值表示任务无需更新。
type Update struct {
	Status    constant.OutboxStatus
	Attempted bool
}

// WithLockedRecord 在同一事务内读取、调用处理函数并写回结果。ctx 在提交前取消时，事务回滚并释放行锁。
// 处理函数与存储操作的错误分别返回。
// pendingType 用于补偿，锁定时再次复核类型和状态，跳过本轮不能处理的记录。
func (s *Store) WithLockedRecord(
	ctx context.Context,
	id int64,
	pendingType *constant.OutboxHandlerType,
	apply func(model.Record) (Update, error),
) (taskErr, storageErr error) {
	if apply == nil {
		return nil, outboxerr.ErrInvalidArgument
	}
	db, masterErr := s.master(ctx)
	if masterErr != nil {
		return nil, masterErr
	}
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer tx.Rollback()

	query := s.clean(tx).Clauses(clause.Locking{
		Strength: "UPDATE",
		Options:  "NOWAIT",
	}).Where("id = ?", id)
	if pendingType != nil {
		query = query.Where("status = ? AND handler_type = ?", constant.OutboxPending, *pendingType)
	}
	row, readErr := s.takeRecord(query)
	if readErr != nil {
		if errors.Is(readErr, gorm.ErrRecordNotFound) {
			if pendingType != nil {
				return nil, nil
			}
			return nil, ErrNotFound
		}
		// MySQL ER_LOCK_NOWAIT / PostgreSQL lock_not_available 表示当前行正在发送。
		if isMySQLLockConflict(readErr) || isPostgreSQLLockConflict(readErr) {
			if pendingType != nil {
				return nil, nil
			}
			return nil, errors.Join(ErrBusy, readErr)
		}
		return nil, readErr
	}
	// 查询成功到开始发布之间也可能发生取消。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, taskErr := apply(row)
	if result.Status == "" {
		return taskErr, nil
	}
	values := map[string]any{
		"status":      result.Status,
		"last_error":  "",
		"updated_at":  gorm.Expr(s.nowSQL()),
		"finished_at": gorm.Expr(s.nowSQL()),
	}
	if result.Attempted {
		values["attempts"] = row.Attempts + 1
	}
	if taskErr != nil {
		values["last_error"] = outboxerr.ErrorText(taskErr)
	}
	if result.Status == constant.OutboxPending {
		values["finished_at"] = nil
	}
	if err := s.clean(tx).Where("id = ?", id).Updates(values).Error; err != nil {
		return taskErr, err
	}
	return taskErr, tx.Commit().Error
}
