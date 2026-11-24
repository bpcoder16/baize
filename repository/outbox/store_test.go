package outbox_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/baize/internal/testdb"
	model "github.com/bpcoder16/baize/model/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
	outbox "github.com/bpcoder16/baize/service/outbox"
	"github.com/bpcoder16/pixiu/logit"
	"gorm.io/gorm"
)

func liveStore(t *testing.T, backend constant.Backend, mysqlLocation ...*time.Location) (*repo.Store, *gorm.DB) {
	t.Helper()
	db := testdb.Open(t, backend, mysqlLocation...)
	store, err := repo.Open(t.Context(), repo.StoreConfig{
		Backend: backend,
		Client:  testdb.Client{Primary: db},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, db
}

func eachDatabase(t *testing.T, test func(*testing.T, *repo.Store, *gorm.DB)) {
	t.Helper()
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			store, db := liveStore(t, backend)
			test(t, store, db)
		})
	}
}

func task() model.Task {
	return model.Task{
		ResourceName: "events",
		HandlerType:  constant.NatsJetStream,
		Payload:      []byte(`{"amount":1}`),
	}
}

func enqueueTask(t *testing.T, s *repo.Store, db *gorm.DB, value model.Task) int64 {
	t.Helper()
	var id int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = s.InsertTx(tx, value)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("登记必须返回数据库生成的正 ID: %d", id)
	}
	return id
}

func enqueue(t *testing.T, s *repo.Store, db *gorm.DB) int64 {
	t.Helper()
	return enqueueTask(t, s, db, task())
}

func TestOpenExisting(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			ctx := t.Context()
			db := testdb.Open(t, backend)
			cfg := repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}}
			if _, err := repo.OpenExisting(ctx, cfg); err == nil || !strings.Contains(err.Error(), "baize_outbox") {
				t.Fatalf("缺表应返回包含表名的错误，得到 %v", err)
			}
			if db.Migrator().HasTable("baize_outbox") {
				t.Fatal("关闭迁移不应创建表")
			}
			if _, err := repo.Open(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			// 空表也应可用。移除一个次要索引，可观察普通副本是否意外执行了迁移。
			const index = "baize_outbox_ready_idx"
			if err := db.Migrator().DropIndex("baize_outbox", index); err != nil {
				t.Fatal(err)
			}
			store, err := repo.OpenExisting(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if db.Migrator().HasIndex("baize_outbox", index) {
				t.Fatal("关闭迁移不应重建索引")
			}
			enqueue(t, store, db)
			if err = db.Exec("ALTER TABLE baize_outbox DROP COLUMN last_error").Error; err != nil {
				t.Fatal(err)
			}
			if existing, openErr := repo.OpenExisting(ctx, cfg); existing != nil || openErr == nil || !strings.Contains(openErr.Error(), "last_error") {
				t.Fatalf("缺少 last_error 列时应拒绝打开仓储，得到 %v", openErr)
			}
		})
	}
}

func TestDatabaseAtomicityAndSnapshot(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := logit.WithContextLogID(context.Background())
		_, enqueueErr := s.InsertTx(db, task())
		if !errors.Is(enqueueErr, repo.ErrNotTransaction) || !errors.Is(enqueueErr, outbox.ErrNotTransaction) {
			t.Fatalf("普通会话必须返回仓储和服务门面共用的事务错误: %v", enqueueErr)
		}
		rollback := errors.New("业务回滚")
		var rolledBack, committed int64
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			rolledBack, err = s.InsertTx(tx, task())
			if err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) || rolledBack <= 0 {
			t.Fatalf("回滚前应取得自增 ID: %d %v", rolledBack, err)
		}
		if _, err := s.TestReadRecord(ctx, rolledBack); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("回滚后不应留下任务: %v", err)
		}
		value := task()
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 历史 Select/Omit/Table 不得污染 Outbox，也不得从事务中逃逸。
			chained := tx.Table("not_a_real_table").Select("id").Omit("payload")
			var err error
			committed, err = s.InsertTx(chained, value)
			return err
		})
		if err != nil || committed <= rolledBack {
			t.Fatalf("自增 ID 应允许回滚留下间隙: %d %d %v", rolledBack, committed, err)
		}
		value.Payload[0] = 'X'
		record, err := s.TestReadRecord(ctx, committed)
		logID, _ := logit.LogIDFromContext(ctx)
		wantPayload := `{"amount":1}`
		if err != nil || record.ID != committed || record.HandlerType != constant.NatsJetStream || record.Status != constant.OutboxPending || record.ResourceName != "events" || string(record.Payload) != wantPayload || record.LogID != logID {
			t.Fatalf("入表快照错误: %+v %v", record, err)
		}
		if time.Since(record.CreatedAt).Abs() > 10*time.Second || record.CreatedAt.Location() != time.UTC {
			t.Fatalf("非 UTC 驱动不应使时间偏移: %v", record.CreatedAt)
		}
		// 不再使用业务唯一键；重复登记同一内容也生成独立任务。
		next := enqueue(t, s, db)
		if next <= committed {
			t.Fatalf("重复登记必须生成新 ID: %d <= %d", next, committed)
		}
	})
}

func TestOptionalLogIDKeepsBusinessTransaction(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		if err := db.Exec("CREATE TABLE business_write (id INT PRIMARY KEY)").Error; err != nil {
			t.Fatal(err)
		}
		boundary := strings.Repeat("界", math.MaxUint16/3)
		for i, tc := range []struct{ name, raw, want string }{
			{"缺失", "", ""},
			{"自定义", "业务-123", "业务-123"},
			{"TEXT上限", boundary, boundary},
			{"超出一字节", boundary + "a", ""},
			{"零字节", "before\x00after", ""},
			{"非法编码", "bad\xff", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx := t.Context()
				if tc.raw != "" {
					ctx = logit.WithContext(ctx)
					logit.AddMeta(ctx, logit.Str(logit.LogId, tc.raw))
				}
				var id int64
				err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("INSERT INTO business_write (id) VALUES (?)", i).Error; err != nil {
						return err
					}
					var err error
					id, err = s.InsertTx(tx, task())
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				row, err := s.TestReadRecord(ctx, id)
				if err != nil || row.LogID != tc.want {
					t.Fatalf("logId 字节数为 %d，期望 %d，读取错误 %v", len(row.LogID), len(tc.want), err)
				}
				var count int64
				if err := db.Table("business_write").Where("id = ?", i).Count(&count).Error; err != nil || count != 1 {
					t.Fatalf("业务记录未提交: %d %v", count, err)
				}
			})
		}
	})
}

func TestMasterUsesCurrentContext(t *testing.T) {
	eachDatabase(t, func(t *testing.T, base *repo.Store, db *gorm.DB) {
		oldContext, cancel := context.WithCancel(t.Context())
		cancel()
		cached := db.Table("not_a_real_table").Where("invalid_column = ?", 1).WithContext(oldContext)
		cfg := *base.TestConfig()
		cfg.Client = testdb.Client{Primary: cached}
		openCtx, cancelOpen := context.WithCancel(t.Context())
		defer cancelOpen()
		store, err := repo.Open(openCtx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		cancelOpen()
		type requestKey struct{}
		ctx := context.WithValue(t.Context(), requestKey{}, "current")
		session, err := store.TestSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if session.Statement.Context.Err() != nil || session.Statement.Context.Value(requestKey{}) != "current" {
			t.Fatal("本次操作未使用新的 Context")
		}
		id := enqueue(t, store, db)
		if _, err := store.TestReadRecord(ctx, id); err != nil {
			t.Fatalf("主库句柄上的历史查询条件影响了本次读取: %v", err)
		}
		if !errors.Is(cached.Statement.Context.Err(), context.Canceled) {
			t.Fatal("不应修改缓存基础会话的 Context")
		}
	})
}

func TestEnqueueCancelledContextRollsBack(t *testing.T) {
	eachDatabase(t, func(t *testing.T, store *repo.Store, db *gorm.DB) {
		err := db.Transaction(func(tx *gorm.DB) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := store.InsertTx(tx.WithContext(ctx), task())
			return err
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("数据库操作必须传播取消: %v", err)
		}
		var count int64
		if err := db.Table("baize_outbox").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("取消的事务不得留下任务: %d %v", count, err)
		}
	})
}

func TestRegisteredSchemaAndRepeatedMigration(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := context.Background()
		id := enqueue(t, s, db)
		// 在临时库恢复旧索引布局，验证现有表可以补建清理索引。
		if err := db.Migrator().DropIndex("baize_outbox", "baize_outbox_cleanup_idx"); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE INDEX baize_outbox_finished_idx ON baize_outbox (status, finished_at, id)").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Open(ctx, *s.TestConfig()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.TestReadRecord(ctx, id); err != nil {
			t.Fatalf("重复迁移不得破坏任务: %v", err)
		}
		columns, err := db.Migrator().ColumnTypes("baize_outbox")
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != 12 {
			t.Fatalf("列数错误: %d", len(columns))
		}
		for _, column := range columns {
			name := column.Name()
			nullable, ok := column.Nullable()
			wantNullable := slices.Contains([]string{"expires_at", "finished_at"}, name)
			if !ok || nullable != wantNullable {
				t.Errorf("%s NULL 属性错误: %v/%v", name, nullable, ok)
			}
			if name == "resource_name" {
				if length, ok := column.Length(); !ok || length != 64 || column.DatabaseTypeName() != "varchar" {
					t.Errorf("resource_name 应为 varchar(64): %s 长度 %d/%v", column.DatabaseTypeName(), length, ok)
				}
			}
			if name == "id" {
				wantType := "bigint"
				if s.TestConfig().Backend == constant.PostgreSQL {
					wantType = "int8"
				}
				if column.DatabaseTypeName() != wantType {
					t.Errorf("ID 必须是 64 位整数: %s", column.DatabaseTypeName())
				}
				if auto, ok := column.AutoIncrement(); !ok || !auto {
					t.Errorf("ID 必须由数据库自增: %v/%v", auto, ok)
				}
			}
			if name == "payload" {
				wantType := "bytea"
				if s.TestConfig().Backend == constant.MySQL {
					wantType = "longblob"
				}
				if column.DatabaseTypeName() != wantType {
					t.Errorf("%s 类型错误: %s", name, column.DatabaseTypeName())
				}
			}
			if slices.Contains([]string{"created_at", "updated_at", "expires_at", "finished_at"}, name) {
				wantType := "timestamptz"
				if s.TestConfig().Backend == constant.MySQL {
					wantType = "timestamp"
				}
				if column.DatabaseTypeName() != wantType {
					t.Errorf("%s 时间类型错误: %s, want %s", name, column.DatabaseTypeName(), wantType)
				}
			}
		}
		var precision []struct {
			DatetimePrecision int
		}
		precisionQuery := "SELECT datetime_precision FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'baize_outbox' AND column_name IN ('created_at','updated_at','expires_at','finished_at')"
		if s.TestConfig().Backend == constant.PostgreSQL {
			precisionQuery = "SELECT datetime_precision FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'baize_outbox' AND column_name IN ('created_at','updated_at','expires_at','finished_at')"
		}
		if err := db.Raw(precisionQuery).Scan(&precision).Error; err != nil {
			t.Fatal(err)
		}
		if len(precision) != 4 {
			t.Fatalf("时间列数错误: %+v", precision)
		}
		for _, column := range precision {
			if column.DatetimePrecision != 6 {
				t.Errorf("时间精度错误: %d", column.DatetimePrecision)
			}
		}
		query := `SELECT collation_name FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = 'baize_outbox' AND column_name = ?`
		wantCollation := "ascii_bin"
		if s.TestConfig().Backend == constant.PostgreSQL {
			query = `SELECT collation_name FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'baize_outbox' AND column_name = ?`
			wantCollation = "C"
		}
		for _, column := range []string{"handler_type", "resource_name", "status"} {
			var collation string
			if err := db.Raw(query, column).Row().Scan(&collation); err != nil {
				t.Fatal(err)
			}
			if collation != wantCollation {
				t.Errorf("%s 排序规则错误: %s", column, collation)
			}
		}
		var matches int64
		if err := db.Table("baize_outbox").Where("resource_name = ? OR status = ?", "Events", "Pending").Count(&matches).Error; err != nil || matches != 0 {
			t.Fatalf("发布目标和状态的查询应区分大小写: %d %v", matches, err)
		}
		if next := enqueue(t, s, db); next <= id {
			t.Fatalf("重复迁移不得重置序列: %d <= %d", next, id)
		}
		indexes, err := db.Migrator().GetIndexes("baize_outbox")
		if err != nil {
			t.Fatal(err)
		}
		for name, columns := range map[string][]string{
			"baize_outbox_ready_idx":    {"status", "id"},
			"baize_outbox_finished_idx": {"status", "finished_at", "id"},
			"baize_outbox_cleanup_idx":  {"handler_type", "status", "created_at", "id"},
		} {
			found := slices.ContainsFunc(indexes, func(index gorm.Index) bool {
				return index.Name() == name && slices.Equal(index.Columns(), columns)
			})
			if s.TestConfig().Backend == constant.PostgreSQL {
				// 当前 GORM PG GetIndexes 按表列顺序返回；以系统目录的真实索引定义验收。
				var definition string
				if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'baize_outbox' AND indexname = ?", name).Scan(&definition).Error; err != nil {
					t.Fatal(err)
				}
				found = strings.Contains(definition, "("+strings.Join(columns, ", ")+")")
			}
			if !found {
				t.Errorf("索引缺失或列顺序错误: %s", name)
			}
		}
	})
}

func TestBusinessAndOutboxRollbackTogether(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		type businessRecord struct {
			ID int64 `gorm:"primaryKey"`
		}
		if err := db.AutoMigrate(&businessRecord{}); err != nil {
			t.Fatal(err)
		}
		var rolledBack, outer int64
		rollback := errors.New("回滚业务与任务")
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&businessRecord{ID: 1}).Error; err != nil {
				return err
			}
			var err error
			rolledBack, err = s.InsertTx(tx, task())
			if err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&businessRecord{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("业务未回滚: %d %v", count, err)
		}
		if _, err := s.TestReadRecord(context.Background(), rolledBack); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("任务未回滚: %v", err)
		}
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&businessRecord{ID: 2}).Error; err != nil {
				return err
			}
			err := tx.Transaction(func(inner *gorm.DB) error {
				var err error
				rolledBack, err = s.InsertTx(inner, task())
				if err != nil {
					return err
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				return err
			}
			outer, err = s.InsertTx(tx, task())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&businessRecord{}).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("外层业务未提交: %d %v", count, err)
		}
		if _, err := s.TestReadRecord(context.Background(), rolledBack); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("保存点回滚未移除任务: %v", err)
		}
		if _, err := s.TestReadRecord(context.Background(), outer); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEnqueueRejectsInvalidTransactionAndTask(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		err := db.Transaction(func(tx *gorm.DB) error {
			oversized := task()
			oversized.Payload = make([]byte, (1<<20)+1)
			if _, err := s.InsertTx(tx, oversized); !errors.Is(err, outbox.ErrInvalidArgument) {
				t.Errorf("超大任务未在入库前拒绝: %v", err)
			}
			if _, err := s.InsertTx(tx.Session(&gorm.Session{DryRun: true}), task()); !errors.Is(err, outbox.ErrInvalidSession) {
				t.Errorf("DryRun 必须拒绝: %v", err)
			}
			wrong := *s.TestConfig()
			wrong.Backend = constant.MySQL
			if wrong.Backend == s.TestConfig().Backend {
				wrong.Backend = constant.PostgreSQL
			}
			if _, err := repo.Open(t.Context(), wrong); !errors.Is(err, outbox.ErrBackendMismatch) {
				t.Errorf("初始化时应拒绝错误方言: %v", err)
			}
			inTx := *s.TestConfig()
			inTx.Client = testdb.Client{Primary: tx}
			if _, err := repo.Open(t.Context(), inTx); !errors.Is(err, outbox.ErrInvalidSession) {
				t.Errorf("初始化时应拒绝事务句柄: %v", err)
			}
			_, err := s.InsertTx(tx, task())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Table("baize_outbox").Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("非法任务产生副作用: %d %v", count, err)
		}
	})
}

func TestInsertTxRejectsOtherMaster(t *testing.T) {
	eachDatabase(t, func(t *testing.T, store *repo.Store, db *gorm.DB) {
		_, other := liveStore(t, store.TestConfig().Backend)
		for _, prepared := range []bool{false, true} {
			session := &gorm.Session{PrepareStmt: prepared}
			err := other.Session(session).Transaction(func(tx *gorm.DB) error {
				id, err := store.InsertTx(tx, task())
				if id != 0 {
					t.Errorf("异库事务返回了任务 ID %d", id)
				}
				return err
			})
			if !errors.Is(err, outbox.ErrInvalidSession) {
				t.Errorf("应拒绝同方言的其他主库，PrepareStmt=%t: %v", prepared, err)
			}
			enqueue(t, store, db.Session(session))
		}
		var count int64
		if err := other.Table("baize_outbox").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("其他主库不应产生任务: count=%d err=%v", count, err)
		}
	})
}

func TestStoreAndWorkerValidateHandlerType(t *testing.T) {
	eachDatabase(t, func(t *testing.T, store *repo.Store, db *gorm.DB) {
		handler := testPublisher(func(context.Context, model.Delivery) error { return nil })
		for _, kind := range []constant.OutboxHandlerType{"", "events ", "x\x00y", "中文", constant.OutboxHandlerType(strings.Repeat("x", 33))} {
			if _, err := outbox.NewWorker(store, kind, handler); !errors.Is(err, outbox.ErrInvalidArgument) {
				t.Errorf("Worker 接受了非法类型 %q: %v", kind, err)
			}
			value := task()
			value.HandlerType = kind
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := store.InsertTx(tx, value)
				return err
			})
			if !errors.Is(err, outbox.ErrInvalidArgument) {
				t.Errorf("仓储应在写入前拒绝非法类型 %q: %v", kind, err)
			}
		}
		value := task()
		value.HandlerType = constant.OutboxHandlerType(strings.Repeat("x", 32))
		if _, err := outbox.NewWorker(store, value.HandlerType, handler); err != nil {
			t.Fatal(err)
		}
		enqueueTask(t, store, db, value)
		var count int64
		if err := db.Table("baize_outbox").Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("只应写入合法类型: count=%d err=%v", count, err)
		}
	})
}

func TestOpenSerializesConcurrentFirstMigration(t *testing.T) {
	for _, backend := range []constant.Backend{constant.MySQL, constant.PostgreSQL} {
		t.Run(string(backend), func(t *testing.T) {
			db := testdb.Open(t, backend)
			cfg := repo.StoreConfig{Backend: backend, Client: testdb.Client{Primary: db}}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			start := make(chan struct{})
			results := make(chan error, 2)
			for range 2 {
				go func() {
					<-start
					_, err := repo.Open(ctx, cfg)
					results <- err
				}()
			}
			close(start)
			for range 2 {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			if !db.Migrator().HasColumn("baize_outbox", "handler_type") {
				t.Fatal("并发启动后应具有完整表结构")
			}
		})
	}
}

func TestWithLockedRecordRejectsNilApply(t *testing.T) {
	// 使用未配置主库的 Store 确认参数错误在访问数据库前返回。
	var s repo.Store
	taskErr, storageErr := s.WithLockedRecord(t.Context(), 1, nil, nil)
	if taskErr != nil || !errors.Is(storageErr, outbox.ErrInvalidArgument) {
		t.Fatalf("空回调应返回参数错误: taskErr=%v storageErr=%v", taskErr, storageErr)
	}
}

func TestPendingScanAndLockedReadRespectHandlerType(t *testing.T) {
	eachDatabase(t, func(t *testing.T, s *repo.Store, db *gorm.DB) {
		ctx := t.Context()
		own := enqueue(t, s, db)
		value := task()
		value.HandlerType = "test_other"
		other := enqueueTask(t, s, db, value)
		kind := constant.NatsJetStream
		var ids []int64
		if err := s.ResumePending(ctx, kind, 1, &repo.PendingCursor{}, func(id int64) error {
			ids = append(ids, id)
			return nil
		}); err != nil || !slices.Equal(ids, []int64{own}) {
			t.Fatalf("补偿扫描越过类型边界: %v %v", ids, err)
		}
		taskErr, storageErr := s.WithLockedRecord(ctx, other, &kind, func(model.Record) (repo.Update, error) {
			t.Fatal("持锁复核必须跳过其他类型的记录")
			return repo.Update{}, nil
		})
		if taskErr != nil || storageErr != nil {
			t.Fatalf("%v, %v", taskErr, storageErr)
		}
	})
}
