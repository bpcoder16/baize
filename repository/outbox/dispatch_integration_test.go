package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	outbox "github.com/bpcoder16/baize/service/outbox"
	"gorm.io/gorm"
)

type testPublisher func(context.Context, model.Delivery) error

func (f testPublisher) Publish(ctx context.Context, d model.Delivery) error { return f(ctx, d) }

func testWorker(t *testing.T, s *repo.Store, handler testPublisher) *outbox.Worker {
	t.Helper()
	w, err := outbox.NewWorker(s, constant.NatsJetStream, handler)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSendAndCronRounds(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		calls := map[int64]int{}
		a := enqueue(t, s, db)
		b := enqueue(t, s, db)
		c := enqueue(t, s, db)
		w := testWorker(t, s, func(_ context.Context, d model.Delivery) error {
			calls[d.ID]++
			if d.ID == a || calls[d.ID] == 1 {
				return errors.New("发送失败")
			}
			return nil
		})
		ctx := context.Background()
		if err := w.Send(ctx, b); err == nil {
			t.Fatal("首次失败应返回错误")
		}
		r, _ := s.TestReadRecord(ctx, b)
		if r.Status != constant.OutboxPending || r.Attempts != 1 || r.LastError == "" {
			t.Fatalf("首次结果错误: %+v", r)
		}
		for round := 1; round <= 2; round++ {
			if err := w.RetryPending(ctx, 1); err == nil {
				t.Fatal("失败应向 cron 返回错误")
			}
			if calls[a] != round {
				t.Fatalf("同轮反复执行: %v", calls)
			}
		}
		for _, id := range []int64{b, c} {
			r, _ := s.TestReadRecord(ctx, id)
			if r.Status != constant.OutboxSucceeded || r.Attempts != 2 || r.LastError != "" || r.FinishedAt == nil {
				t.Fatalf("补偿错误: %+v", r)
			}
			if err := w.Send(ctx, id); err != nil {
				t.Fatal(err)
			}
			if calls[id] != 2 {
				t.Fatal("已成功任务再次执行")
			}
		}
	})
}

// 主动发送和补偿共用状态转换，包装后的无效资源错误也进入终态。
func TestWrappedInvalidResourceStopsAfterFirstAttempt(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		id := enqueue(t, s, db)
		calls := 0
		handle := func(context.Context, model.Delivery) error {
			calls++
			return fmt.Errorf("removed target: %w", outboxerr.ErrInvalidResource)
		}
		ctx := t.Context()
		w := testWorker(t, s, handle)
		taskErr := w.Send(ctx, id)
		if !errors.Is(taskErr, outbox.ErrInvalidResource) {
			t.Fatalf("首次投递错误: %v", taskErr)
		}
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if err := w.Send(ctx, id); !errors.Is(err, outbox.ErrInvalidResource) {
			t.Fatalf("终态应返回原错误类型: %v", err)
		}
		row, err := s.TestReadRecord(ctx, id)
		if err != nil || calls != 1 || row.Status != constant.OutboxFailed || row.Attempts != 1 || row.FinishedAt == nil || row.LastError != taskErr.Error() {
			t.Fatalf("无效目标应只尝试一次并保存完整原因: calls=%d row=%+v err=%v", calls, row, err)
		}
	})
}

func TestRetryPendingContinuesAfterAttemptTimeout(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		first := enqueue(t, s, db)
		second := enqueue(t, s, db)
		calls := map[int64]int{}
		w := testWorker(t, s, func(ctx context.Context, d model.Delivery) error {
			calls[d.ID]++
			if d.ID == first {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("单条补偿必须有独立的 5 秒期限")
					return errors.New("缺少单条期限")
				}
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := w.RetryPending(ctx, 1); !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			t.Fatalf("单条超时应保留错误但不取消整轮: %v，整轮: %v", err, ctx.Err())
		}
		if calls[first] != 1 || calls[second] != 1 {
			t.Fatalf("超时不能阻挡后续页，也不能在本轮重复尝试: %v", calls)
		}
		row, err := s.TestReadRecord(ctx, first)
		if err != nil || row.Status != constant.OutboxPending || row.Attempts != 0 || row.LastError != "" {
			t.Fatalf("单条超时应回滚本次尝试: %+v %v", row, err)
		}
		row, err = s.TestReadRecord(ctx, second)
		if err != nil || row.Status != constant.OutboxSucceeded {
			t.Fatalf("后续健康任务应成功: %+v %v", row, err)
		}
	})
}

func TestRetryPendingResumesAfterRoundCancellation(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		first := enqueue(t, s, db)
		second := enqueue(t, s, db)
		var attempts []int64
		var cancelRound context.CancelFunc
		w := testWorker(t, s, func(ctx context.Context, d model.Delivery) error {
			attempts = append(attempts, d.ID)
			if d.ID == first && cancelRound != nil {
				cancelRound()
				return ctx.Err()
			}
			return nil
		})
		// 队首耗尽整轮预算，期间新任务持续入队。先发出原上界内的健康任务，再回头重试。
		ids := []int64{first, second}
		for _, want := range []int64{first, second, first} {
			ctx, cancel := context.WithCancel(t.Context())
			cancelRound = cancel
			err := w.RetryPending(ctx, 1)
			cancel()
			if len(attempts) != 1 || attempts[0] != want {
				t.Fatalf("期望本轮尝试 %d，实际 %v，错误 %v", want, attempts, err)
			}
			if want == first && !errors.Is(err, context.Canceled) {
				t.Fatalf("队首应耗尽整轮预算: %v", err)
			}
			if want == second {
				if err != nil {
					t.Fatal(err)
				}
				row, err := s.TestReadRecord(t.Context(), second)
				if err != nil || row.Status != constant.OutboxSucceeded {
					t.Fatalf("队首持续超时期间，第二条应已投递: %+v %v", row, err)
				}
			}
			attempts = nil
			ids = append(ids, enqueue(t, s, db))
		}
		cancelRound = nil
		for range 2 {
			if err := w.RetryPending(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range ids {
			row, err := s.TestReadRecord(t.Context(), id)
			if err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 {
				t.Errorf("任务 %d 应成功提交一次: row=%+v err=%v", id, row, err)
			}
		}
	})
}

func TestPostgreSQLRetryStoresErrorWithNUL(t *testing.T) {
	store, db := liveStore(t, constant.PostgreSQL)
	first := enqueue(t, store, db)
	second := enqueue(t, store, db)
	publishErr := errors.New("上游\x00异常")
	worker := testWorker(t, store, func(_ context.Context, d model.Delivery) error {
		if d.ID == first {
			return publishErr
		}
		return nil
	})
	if err := worker.RetryPending(t.Context(), 1); !errors.Is(err, publishErr) {
		t.Fatalf("补偿应保留原始发布错误: %v", err)
	}
	row, err := store.TestReadRecord(t.Context(), first)
	if err != nil || row.Status != constant.OutboxPending || row.Attempts != 1 || row.LastError != "上游�异常" {
		t.Fatalf("失败原因和尝试次数应一同提交: %+v %v", row, err)
	}
	row, err = store.TestReadRecord(t.Context(), second)
	if err != nil || row.Status != constant.OutboxSucceeded {
		t.Fatalf("单条发布失败不应阻止后续补偿: %+v %v", row, err)
	}
}

func TestRetryPendingConcurrentCallsKeepRowLock(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		first := enqueue(t, s, db)
		second := enqueue(t, s, db)
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		w := testWorker(t, s, func(_ context.Context, d model.Delivery) error {
			if d.ID == first {
				close(entered)
				<-release
			}
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.RetryPending(ctx, 1) }()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("未开始补偿")
		}
		// 模拟分布式锁失租后的重叠调用：仍由数据库跳过持锁行，不增加本地互斥。
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatalf("重叠调用应跳过持锁行并继续处理: %v", err)
		}
		if row, err := s.TestReadRecord(ctx, second); err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 {
			t.Fatalf("后续任务应完成一次发送: %+v %v", row, err)
		}
		once.Do(func() { close(release) })
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatalf("并发调用后仍应能够继续扫描: %v", err)
		}
	})
}

func TestExpiryAndNoExpiry(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := context.Background()
		calls := map[int64]int{}
		w := testWorker(t, s, func(_ context.Context, d model.Delivery) error {
			calls[d.ID]++
			return errors.New("离线")
		})
		ids := map[string]int64{}
		laterExpiresAt := time.Now().Add(2 * time.Second)
		for _, name := range []string{"expired", "later", "forever"} {
			value := task()
			if name == "expired" {
				value.ExpiresAt = time.Now().Add(-time.Hour)
			}
			if name == "later" {
				value.ExpiresAt = laterExpiresAt
			}
			ids[name] = enqueueTask(t, s, db, value)
		}
		if err := w.Send(ctx, ids["expired"]); !errors.Is(err, outbox.ErrExpired) {
			t.Fatalf("过期应拒绝: %v", err)
		}
		if err := w.Send(ctx, ids["later"]); err == nil {
			t.Fatal("应失败")
		}
		time.Sleep(time.Until(laterExpiresAt))
		for range 2 {
			_ = w.RetryPending(ctx, 1)
		}
		for _, id := range []int64{ids["expired"], ids["later"]} {
			r, _ := s.TestReadRecord(ctx, id)
			if r.Status != constant.OutboxFailed || r.LastError != outbox.ErrExpired.Error() || r.FinishedAt == nil {
				t.Fatalf("过期未标记: %+v", r)
			}
		}
		if calls[ids["expired"]] != 0 || calls[ids["later"]] != 1 || calls[ids["forever"]] != 2 {
			t.Fatalf("过期执行或重试异常: %v", calls)
		}
		r, _ := s.TestReadRecord(ctx, ids["forever"])
		if r.ExpiresAt != nil || r.Status != constant.OutboxPending {
			t.Fatalf("零值应不过期: %+v", r)
		}
	})
}

func TestExpiryAfterLockedRead(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		value := task()
		value.ExpiresAt = time.Now().Add(time.Second).Truncate(time.Microsecond)
		id := enqueueTask(t, s, db, value)
		const callback = "outbox_test:delay_locked_read"
		if err := db.Callback().Query().After("gorm:after_query").Register(callback, func(tx *gorm.DB) {
			if _, locking := tx.Statement.Clauses["FOR"]; locking && tx.Error == nil {
				// 模拟查询结果延迟返回；与数据库一致按绝对时刻等待，唤醒后确认已到期。
				for time.Now().Before(value.ExpiresAt) {
					time.Sleep(time.Until(value.ExpiresAt))
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
		calls := 0
		w := testWorker(t, s, func(context.Context, model.Delivery) error {
			calls++
			return nil
		})
		ctx := context.Background()
		if err := w.Send(ctx, id); !errors.Is(err, outbox.ErrExpired) {
			t.Fatalf("读取期间过期的任务应拒绝发送: %v", err)
		}
		row, err := s.TestReadRecord(ctx, id)
		if err != nil || row.Status != constant.OutboxFailed || row.Attempts != 0 || calls != 0 || row.FinishedAt == nil || row.LastError != outbox.ErrExpired.Error() {
			t.Fatalf("过期任务应保存失败原因且不尝试发送: %+v calls=%d err=%v", row, calls, err)
		}
	})
}

func TestSendUsesOneReadAndOneUpdate(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		reads, writes := 0, 0
		const callback = "outbox_test:count_dispatch_sql"
		if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "baize_outbox" {
				reads++
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
		if err := db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "baize_outbox" {
				writes++
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
		sendErr := errors.New("发送失败")
		for _, tc := range []struct {
			name       string
			expiry     time.Time
			handlerErr error
			wantErr    error
			status     constant.OutboxStatus
			attempts   int64
		}{
			{
				name:     "成功",
				expiry:   time.Now().Add(time.Hour),
				status:   constant.OutboxSucceeded,
				attempts: 1,
			},
			{
				name:       "发送失败",
				handlerErr: sendErr,
				wantErr:    sendErr,
				status:     constant.OutboxPending,
				attempts:   1,
			},
			{
				name:    "已过期",
				expiry:  time.Now().Add(-time.Hour),
				wantErr: outbox.ErrExpired,
				status:  constant.OutboxFailed,
			},
			{
				name:     "不过期",
				status:   constant.OutboxSucceeded,
				attempts: 1,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				value := task()
				value.ExpiresAt = tc.expiry
				id := enqueueTask(t, s, db, value)
				calls := 0
				w := testWorker(t, s, func(context.Context, model.Delivery) error {
					calls++
					if writes != 0 {
						t.Errorf("发送前应仅加锁读取，更新次数为 %d", writes)
					}
					return tc.handlerErr
				})
				reads, writes = 0, 0
				ctx := context.Background()
				if err := w.Send(ctx, id); !errors.Is(err, tc.wantErr) {
					t.Fatalf("发送结果错误: %v, want %v", err, tc.wantErr)
				}
				if reads != 1 || writes != 1 {
					t.Errorf("单条任务应仅查询和更新各一次: reads=%d writes=%d", reads, writes)
				}
				row, err := s.TestReadRecord(ctx, id)
				if err != nil || row.Status != tc.status || row.Attempts != tc.attempts || int64(calls) != tc.attempts {
					t.Fatalf("结果与次数必须一起保存，过期不应调用 Handler: %+v calls=%d err=%v", row, calls, err)
				}
				wantError := ""
				if tc.wantErr != nil {
					wantError = tc.wantErr.Error()
				}
				if row.LastError != wantError || (row.FinishedAt != nil) != (tc.status != constant.OutboxPending) {
					t.Fatalf("错误原因或完成时间不符: %+v", row)
				}
			})
		}
	})
}

func TestRowLocksAcrossStoresAndConcurrentTasks(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		locked := enqueue(t, s, db)
		direct := enqueue(t, s, db)
		retried := enqueue(t, s, db)
		// 在清理窗口外只观察发送 SQL，保证主动发送与补偿均使用 NOWAIT 获取行锁。
		storeHour(s, 12)
		const callback = "outbox_test:check_nowait"
		if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
			if _, locking := tx.Statement.Clauses["FOR"]; locking && !strings.HasSuffix(tx.Statement.SQL.String(), " FOR UPDATE NOWAIT") {
				t.Errorf("行锁查询应使用 NOWAIT: %s", tx.Statement.SQL.String())
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		w := testWorker(t, s, func(_ context.Context, d model.Delivery) error {
			if d.ID == locked {
				close(entered)
				<-release
			}
			return nil
		})
		// 使用不同 Store，确保互斥来自数据库而非进程内共享对象。
		other, err := repo.Open(t.Context(), *s.TestConfig())
		if err != nil {
			t.Fatal(err)
		}
		otherWorker := testWorker(t, other, func(_ context.Context, d model.Delivery) error {
			if d.ID == locked {
				t.Error("已加锁任务不应进入第二个 Handler")
			}
			return nil
		})
		done := make(chan error, 1)
		go func() { done <- w.Send(ctx, locked) }()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("未进入发送")
		}
		if err := otherWorker.Send(ctx, locked); !errors.Is(err, outbox.ErrBusy) {
			t.Fatalf("同一行应立即返回 ErrBusy: %v", err)
		}
		if err := w.Send(ctx, direct); err != nil {
			t.Fatalf("同一 Store 的不同任务应可并发发送: %v", err)
		}
		if err := otherWorker.RetryPending(ctx, 1); err != nil {
			t.Fatalf("补偿应跳过已锁行并继续处理其他任务: %v", err)
		}
		if row, err := s.TestReadRecord(ctx, retried); err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 {
			t.Fatalf("未补偿其他任务: %+v %v", row, err)
		}
		once.Do(func() { close(release) })
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := otherWorker.Send(ctx, locked); err != nil {
			t.Fatalf("锁释放后已成功任务应直接返回: %v", err)
		}
	})
}

func TestResultWriteFailureAndHandlerErrors(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := context.Background()
		calls := 0
		var panicID int64
		w := testWorker(t, s, func(_ context.Context, d model.Delivery) error {
			calls++
			if d.ID == panicID {
				panic("异常")
			}
			return nil
		})
		resultID := enqueue(t, s, db)
		sendCtx, cancelSend := context.WithCancel(ctx)
		defer cancelSend()
		writeErr := errors.New("回写失败")
		const callback = "outbox_test:fail_result"
		fail := true
		err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			values, ok := tx.Statement.Dest.(map[string]any)
			if ok && values["status"] == constant.OutboxSucceeded && fail {
				fail = false
				cancelSend()
				tx.AddError(writeErr)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
		if err := w.Send(sendCtx, resultID); !errors.Is(err, writeErr) || errors.Is(err, context.Canceled) {
			t.Fatalf("回写应返回 GORM 的错误: %v", err)
		}
		r, _ := s.TestReadRecord(ctx, resultID)
		if r.Status != constant.OutboxPending || r.Attempts != 0 || calls != 1 {
			t.Fatalf("不能误标成功或立即重发: %+v", r)
		}
		if err := w.RetryPending(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatal("未补偿")
		}

		panicID = enqueue(t, s, db)
		if err := w.Send(ctx, panicID); err == nil {
			t.Fatal("panic 应返回错误")
		}
		r, _ = s.TestReadRecord(ctx, panicID)
		if r.Status != constant.OutboxPending || r.LastError == "" {
			t.Fatalf("panic 丢失: %+v", r)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := w.Send(cancelled, panicID); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestCancelledSendReleasesLockForRetry(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		id := enqueue(t, s, db)
		entered := make(chan struct{})
		release := make(chan struct{})
		w := testWorker(t, s, func(ctx context.Context, _ model.Delivery) error {
			close(entered)
			<-release
			return ctx.Err()
		})
		done := make(chan struct{})
		var sendErr error
		go func() {
			defer close(done)
			sendErr = w.Send(ctx, id)
		}()
		finish := sync.OnceFunc(func() {
			cancel()
			close(release)
			<-done
		})
		defer finish()
		waitCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		select {
		case <-entered:
		case <-waitCtx.Done():
			t.Fatal("未进入 Handler")
		}
		cancel()

		// 等待数据库完成回滚，再让补偿使用 NOWAIT 取锁。此时原 Handler 仍在等待。
		var row model.Record
		err := db.WithContext(waitCtx).Transaction(func(tx *gorm.DB) error {
			return tx.Raw("SELECT * FROM baize_outbox WHERE id = ? FOR UPDATE", id).Scan(&row).Error
		})
		if err != nil || row.Status != constant.OutboxPending || row.Attempts != 0 || row.LastError != "" {
			t.Fatalf("取消应释放行锁并保留已提交的任务: %+v %v", row, err)
		}
		retry := testWorker(t, s, func(context.Context, model.Delivery) error { return nil })
		if err := retry.RetryPending(waitCtx, 10); err != nil {
			t.Fatal(err)
		}
		finish()
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("原发送应返回取消错误: %v", sendErr)
		}
		row, err = s.TestReadRecord(waitCtx, id)
		if err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 || row.LastError != "" {
			t.Fatalf("原发送应保留补偿已提交的结果: %+v %v", row, err)
		}
	})
}

func TestRetryCommitsEachTaskIndependently(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		first := enqueue(t, s, db)
		second := enqueue(t, s, db)
		third := enqueue(t, s, db)
		w := testWorker(t, s, func(ctx context.Context, d model.Delivery) error {
			if d.ID == second {
				row, err := s.TestReadRecord(ctx, first)
				if err != nil || row.Status != constant.OutboxSucceeded {
					t.Errorf("处理下一条前必须提交上一条: %+v %v", row, err)
				}
			}
			return nil
		})
		const callback = "outbox_test:fail_second_result"
		writes := 0
		err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			values, ok := tx.Statement.Dest.(map[string]any)
			if ok && values["status"] == constant.OutboxSucceeded {
				writes++
				if writes == 2 {
					tx.AddError(errors.New("第二条回写失败"))
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
		ctx := context.Background()
		if err := w.RetryPending(ctx, 10); err == nil {
			t.Fatal("第二条存储失败应结束本轮")
		}
		if row, err := s.TestReadRecord(ctx, first); err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 {
			t.Fatalf("后续失败不得回滚已完成任务: %+v %v", row, err)
		}
		if row, err := s.TestReadRecord(ctx, second); err != nil || row.Status != constant.OutboxPending || row.Attempts != 0 {
			t.Fatalf("失败事务必须整体回滚: %+v %v", row, err)
		}
		if row, err := s.TestReadRecord(ctx, third); err != nil || row.Status != constant.OutboxPending || row.Attempts != 0 {
			t.Fatalf("回写失败应在处理下一条任务前结束本轮: %+v %v", row, err)
		}
		if err := w.RetryPending(ctx, 10); err != nil {
			t.Fatalf("下次应继续处理回写失败的任务: %v", err)
		}
		if row, err := s.TestReadRecord(ctx, second); err != nil || row.Status != constant.OutboxSucceeded || row.Attempts != 1 {
			t.Fatalf("回写失败的任务下轮仍应处理: %+v %v", row, err)
		}
	})
}

func TestSendCancellationWhileWaitingForConnection(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		id := enqueue(t, s, db)
		pool, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(1)
		conn, err := pool.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		w := testWorker(t, s, func(context.Context, model.Delivery) error {
			t.Error("未取得连接时不得调用 Handler")
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.Send(ctx, id) }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("等待连接时应响应调用方期限: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("等待连接时未响应取消")
		}
	})
}
