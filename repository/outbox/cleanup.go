package outbox

import (
	"context"
	"database/sql"
	"time"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// now 已按 Store 时区表示。
func inCleanupWindow(now time.Time) bool {
	return now.Hour() >= 2 && now.Hour() < 4
}

func (s *Store) cleanFinished(ctx context.Context, db *gorm.DB, handlerType constant.OutboxHandlerType, batchSize int) error {
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Truncate(time.Microsecond)
	for _, state := range []constant.OutboxStatus{constant.OutboxSucceeded, constant.OutboxFailed} {
		for inCleanupWindow(time.Now().In(s.cfg.Location)) {
			// 限制单批占用连接和等待锁的时间，已提交批次在取消或后续失败时仍有效。
			batchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			count, err := s.deleteFinishedBatch(db.WithContext(batchCtx), handlerType, state, cutoff, batchSize)
			cancel()
			if err != nil {
				return err
			}
			if count < batchSize {
				break
			}
		}
	}
	return nil
}

func (s *Store) deleteFinishedBatch(db *gorm.DB, handlerType constant.OutboxHandlerType, state constant.OutboxStatus, cutoff time.Time, limit int) (int, error) {
	var ids []int64
	// READ COMMITTED 避免 InnoDB 在常规范围扫描中持有间隙锁。
	err := db.Transaction(func(tx *gorm.DB) error {
		// 查询和删除分别从同一筛选条件派生，删除时再次校验终态及时间。
		eligible := tableSession(tx, s.table).Where("handler_type = ? AND status = ? AND created_at < ?", handlerType, state, cutoff).Session(&gorm.Session{})
		batch := eligible
		if s.cfg.Backend == constant.MySQL {
			// 按清理索引顺序读取，避免 MySQL 先锁定全部候选行再排序。
			batch = batch.Table("`baize_outbox` FORCE INDEX (`baize_outbox_cleanup_idx`)")
		}
		err := batch.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Order("created_at, id").Limit(limit).Pluck("id", &ids).Error
		if err != nil || len(ids) == 0 {
			return err
		}
		return eligible.Where("id IN ?", ids).Delete(s.tableModel()).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return len(ids), err
}

// CleanFinished 在清理窗口内分批删除创建超过 30 天的终态任务。
func (s *Store) CleanFinished(ctx context.Context, handlerType constant.OutboxHandlerType, batchSize int) error {
	if batchSize <= 0 {
		return outboxerr.ErrInvalidArgument
	}
	if !inCleanupWindow(time.Now().In(s.cfg.Location)) {
		return nil
	}
	db, err := s.master(ctx)
	if err != nil {
		return err
	}
	return s.cleanFinished(ctx, db, handlerType, batchSize)
}
