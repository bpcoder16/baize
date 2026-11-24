package outbox

import (
	"context"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	"gorm.io/gorm"
)

// PendingCursor 记录一次指定类型的扫描进度。零值开始新一遍，复制值可取得独立快照。
type PendingCursor struct {
	after int64
	upper int64
}

// ResumePending 使用从库会话继续扫描同一 Store 和类型的待发任务。取消后保留进度，扫完后游标回到零值。
// 调用期间由调用方独占 cursor。visit 返回错误时保留该行供下轮重试，整轮取消时则越过本次尝试。
func (s *Store) ResumePending(ctx context.Context, handlerType constant.OutboxHandlerType, batchSize int, cursor *PendingCursor, visit func(int64) error) error {
	if ctx == nil || batchSize <= 0 || cursor == nil || visit == nil {
		return outboxerr.ErrInvalidArgument
	}
	db, sessionErr := s.session(ctx, DBClient.SlaveDB)
	if sessionErr != nil {
		return sessionErr
	}
	pending := db.Where("status = ? AND handler_type = ?", constant.OutboxPending, handlerType).Session(&gorm.Session{})
	if cursor.after == cursor.upper {
		// 扫描跨轮时沿用原上界，即使新任务持续入队，也能回到较小的 ID。
		var upper []int64
		if err := pending.Order("id DESC").Limit(1).Pluck("id", &upper).Error; err != nil {
			return err
		}
		*cursor = PendingCursor{}
		if len(upper) == 0 {
			return ctx.Err()
		}
		cursor.upper = upper[0]
	}
	for cursor.after < cursor.upper {
		var ids []int64
		if err := pending.Where("id > ? AND id <= ?", cursor.after, cursor.upper).Order("id").Limit(batchSize).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := visit(id)
			if err == nil || ctx.Err() != nil {
				cursor.after = id
			}
			if err != nil {
				return err
			}
		}
	}
	*cursor = PendingCursor{}
	return ctx.Err()
}
