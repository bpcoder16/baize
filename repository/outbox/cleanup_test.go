package outbox_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	"github.com/bpcoder16/pixiu/logit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestCleanupWindow(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	open := time.Date(2026, 11, 13, 2, 0, 0, 0, loc)
	closeAt := open.Add(2 * time.Hour)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{open.Add(-time.Second), false},
		{open.UTC(), true},
		{closeAt.Add(-time.Microsecond), true},
		{closeAt, false},
		{open.AddDate(0, 0, 1), true},
	} {
		if got := repo.InCleanupWindow(tc.now.In(loc)); got != tc.want {
			t.Errorf("Store 当地时间 %v 得到 %t，期望 %t", tc.now.In(loc), got, tc.want)
		}
	}
}

// 将当前时刻置于测试指定的当地整点附近，真实数据库测试可以在任意时段执行。
func storeHour(s *repo.Store, hour int) {
	now := time.Now().UTC()
	target := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	s.TestConfig().Location = time.FixedZone("测试时区", int(target.Sub(now)/time.Second))
}

func cleanupTask(t *testing.T, s *repo.Store, db *gorm.DB, status constant.OutboxStatus, createdAt time.Time, finishedAt *time.Time) int64 {
	t.Helper()
	id := enqueue(t, s, db)
	err := db.Table("baize_outbox").Where("id = ?", id).Updates(map[string]any{
		"status":      status,
		"created_at":  createdAt,
		"finished_at": finishedAt,
	}).Error
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRetryPendingCleansFinishedInBatches(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := context.Background()
		// 覆盖刚完成或缺少完成时间的旧任务。
		now := time.Now()
		old := now.Add(-30*24*time.Hour - time.Minute)
		var expired []int64
		for range 3 {
			for _, status := range []constant.OutboxStatus{constant.OutboxSucceeded, constant.OutboxFailed} {
				expired = append(expired, cleanupTask(t, s, db, status, old, &now))
			}
		}
		recent := cleanupTask(t, s, db, constant.OutboxSucceeded, now.Add(-30*24*time.Hour+time.Minute), &now)
		other := cleanupTask(t, s, db, constant.OutboxSucceeded, old, &now)
		if err := db.Table("baize_outbox").Where("id = ?", other).Update("handler_type", "other").Error; err != nil {
			t.Fatal(err)
		}
		expired = append(expired, cleanupTask(t, s, db, constant.OutboxFailed, old, nil))
		w := testWorker(t, s, func(context.Context, model.Delivery) error {
			t.Fatal("所有任务均已结束")
			return nil
		})

		storeHour(s, 12)
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatal(err)
		}
		for _, id := range expired {
			if _, err := s.TestReadRecord(ctx, id); err != nil {
				t.Fatalf("窗口外误删 %d: %v", id, err)
			}
		}
		storeHour(s, 3)
		for range 2 {
			if err := w.RetryPending(ctx, 1); err != nil {
				t.Fatal(err)
			}
			for _, id := range expired {
				if _, err := s.TestReadRecord(ctx, id); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("单轮应清完多批，即使没有 pending: id=%d err=%v", id, err)
				}
			}
			for _, id := range []int64{recent, other} {
				if _, err := s.TestReadRecord(ctx, id); err != nil {
					t.Fatalf("误删保留记录 %d: %v", id, err)
				}
			}
		}
	})
}

func TestRetryPendingCleanupSkipsLockedAndPending(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		storeHour(s, 3)
		ctx := context.Background()
		old := time.Now().AddDate(0, -2, 0)
		locked := cleanupTask(t, s, db, constant.OutboxSucceeded, old, nil)
		free := cleanupTask(t, s, db, constant.OutboxSucceeded, old, nil)
		pending := cleanupTask(t, s, db, constant.OutboxPending, old, nil)
		sendErr := errors.New("发送失败")
		w := testWorker(t, s, func(context.Context, model.Delivery) error { return sendErr })

		tx := db.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		defer tx.Rollback()
		if err := tx.Table("baize_outbox").Clauses(clause.Locking{Strength: "UPDATE"}).First(&model.Record{}, locked).Error; err != nil {
			t.Fatal(err)
		}
		if err := w.RetryPending(ctx, 1); !errors.Is(err, sendErr) {
			t.Fatalf("应保留补偿错误: %v", err)
		}
		if _, err := s.TestReadRecord(ctx, free); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("补偿错误或其他记录被锁不应挡住清理: %v", err)
		}
		for _, id := range []int64{locked, pending} {
			if _, err := s.TestReadRecord(ctx, id); err != nil {
				t.Fatalf("误删持锁或待重试记录 %d: %v", id, err)
			}
		}
		if err := tx.Rollback().Error; err != nil {
			t.Fatal(err)
		}
		if err := w.RetryPending(ctx, 1); !errors.Is(err, sendErr) {
			t.Fatal(err)
		}
		if _, err := s.TestReadRecord(ctx, locked); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("下轮应回收已解锁记录: %v", err)
		}
	})
}

func TestCleanupBatchKeepsCutoffAndPending(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := context.Background()
		cutoff := time.Now().Truncate(time.Microsecond)
		old := cutoff.Add(-time.Microsecond)
		oldID := cleanupTask(t, s, db, constant.OutboxFailed, old, nil)
		equalID := cleanupTask(t, s, db, constant.OutboxFailed, cutoff, nil)
		pendingID := cleanupTask(t, s, db, constant.OutboxPending, old, nil)
		if n, err := repo.DeleteFinishedBatch(s, db.WithContext(ctx), constant.NatsJetStream, constant.OutboxFailed, cutoff, 10); err != nil || n != 1 {
			t.Fatalf("应只删除截止点前的已结束记录: n=%d err=%v", n, err)
		}
		if _, err := s.TestReadRecord(ctx, oldID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("截止点前一微秒的记录应删除: %v", err)
		}
		for _, id := range []int64{equalID, pendingID} {
			if _, err := s.TestReadRecord(ctx, id); err != nil {
				t.Fatalf("应保留记录 %d: %v", id, err)
			}
		}
	})
}

func TestRetryPendingCleanupCommitsBeforeCancellation(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		storeHour(s, 3)
		second := cleanupTask(t, s, db, constant.OutboxFailed, time.Now().AddDate(0, -2, 0), nil)
		first := cleanupTask(t, s, db, constant.OutboxFailed, time.Now().AddDate(0, -3, 0), nil)
		w := testWorker(t, s, func(context.Context, model.Delivery) error { return nil })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const callback = "outbox_test:cancel_second_delete"
		batches := 0
		if err := db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
			batches++
			if batches == 2 {
				cancel()
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Delete().Remove(callback) })
		if err := w.RetryPending(ctx, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("第二批取消应结束调用: %v", err)
		}
		ctx = context.Background()
		if _, err := s.TestReadRecord(ctx, first); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("已提交的首批应保留删除结果: %v", err)
		}
		if _, err := s.TestReadRecord(ctx, second); err != nil {
			t.Fatalf("取消的批次应保留记录: %v", err)
		}
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TestReadRecord(ctx, second); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("下轮应能继续清理: %v", err)
		}
	})
}

func TestRetryPendingConcurrentCleanupAllowsSending(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		storeHour(s, 3)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		old := time.Now().Add(-31 * 24 * time.Hour)
		var ids []int64
		for range 5 {
			ids = append(ids, cleanupTask(t, s, db, constant.OutboxSucceeded, old, nil))
		}
		w := testWorker(t, s, func(context.Context, model.Delivery) error { return nil })
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		var batches atomic.Int32
		const callback = "outbox_test:hold_first_cleanup_batch"
		if err := db.Callback().Delete().After("gorm:delete").Register(callback, func(tx *gorm.DB) {
			if tx.Error == nil && batches.Add(1) == 1 {
				// 第一批已删除但尚未提交，验证其他清理和发送能独立推进。
				close(entered)
				select {
				case <-release:
				case <-tx.Statement.Context.Done():
					tx.AddError(tx.Statement.Context.Err())
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Delete().Remove(callback) })
		var firstErr error
		done := make(chan struct{})
		go func() {
			firstErr = w.RetryPending(ctx, 2)
			close(done)
		}()
		defer func() {
			releaseOnce.Do(func() { close(release) })
			<-done
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("清理未进入首批事务")
		}
		fresh := enqueue(t, s, db)
		if err := w.Send(ctx, fresh); err != nil {
			t.Fatalf("清理持锁期间应能登记并发送新任务: %v", err)
		}
		if err := w.RetryPending(ctx, 2); err != nil {
			t.Fatalf("重叠清理应跳过未提交批次并继续: %v", err)
		}
		for _, id := range ids[2:] {
			if _, err := s.TestReadRecord(ctx, id); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("其他批次应可完成删除: id=%d err=%v", id, err)
			}
		}
		releaseOnce.Do(func() { close(release) })
		<-done
		if firstErr != nil {
			t.Fatal(firstErr)
		}
		for _, id := range ids {
			if _, err := s.TestReadRecord(ctx, id); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("并发清理结束后应清完历史: id=%d err=%v", id, err)
			}
		}
		if row, err := s.TestReadRecord(ctx, fresh); err != nil || row.Status != constant.OutboxSucceeded {
			t.Fatalf("新完成的任务应保留: %+v %v", row, err)
		}
	})
}

func TestRetryPendingCleanupFailureRollsBackAndLogs(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		storeHour(s, 3)
		ctx := context.Background()
		old := time.Now().Add(-31 * 24 * time.Hour)
		first := cleanupTask(t, s, db, constant.OutboxFailed, old, nil)
		second := cleanupTask(t, s, db, constant.OutboxFailed, old, nil)
		pending := enqueue(t, s, db)
		sendErr := errors.New("发送仍然失败")
		w := testWorker(t, s, func(context.Context, model.Delivery) error { return sendErr })
		var output bytes.Buffer
		logger := logit.MustNew(
			logit.OptEncoder(logit.DefaultJSONEncoder),
			logit.OptWriter(logit.NewWriter(&output)),
		)
		previous := logit.Default()
		logit.SetDefault(logger)
		t.Cleanup(func() {
			logit.SetDefault(previous)
			_ = logit.Close(logger)
		})
		const callback = "outbox_test:fail_second_cleanup_batch"
		batches := 0
		if err := db.Callback().Delete().After("gorm:delete").Register(callback, func(tx *gorm.DB) {
			batches++
			if batches == 2 {
				// 删除 SQL 已执行后的错误必须回滚本批，首批提交结果应继续保留。
				tx.AddError(errors.New("清理删除回写失败"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Delete().Remove(callback) })
		if err := w.RetryPending(ctx, 1); !errors.Is(err, sendErr) {
			t.Fatalf("清理失败应保留原补偿错误: %v", err)
		}
		if !strings.Contains(output.String(), "OutboxCleanupFailed") ||
			!strings.Contains(output.String(), "清理删除回写失败") {
			t.Fatalf("清理失败必须有日志: %s", output.String())
		}
		if _, err := s.TestReadRecord(ctx, first); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("首批应已提交: %v", err)
		}
		for _, id := range []int64{second, pending} {
			if _, err := s.TestReadRecord(ctx, id); err != nil {
				t.Fatalf("失败批次和 pending 应保留: id=%d err=%v", id, err)
			}
		}
		if err := w.RetryPending(ctx, 1); !errors.Is(err, sendErr) {
			t.Fatal(err)
		}
		if _, err := s.TestReadRecord(ctx, second); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("下轮应重新清理已回滚的批次: %v", err)
		}
	})
}
