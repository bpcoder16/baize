// Package outbox 负责事务发件箱的表初始化、待发扫描、事务写入与持锁更新。
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bpcoder16/baize/constant"
	outboxerr "github.com/bpcoder16/baize/error/outbox"
	mysqloutbox "github.com/bpcoder16/baize/model/entity/mysql/outbox"
	pgoutbox "github.com/bpcoder16/baize/model/entity/postgresql/outbox"
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/baize/repository/internal/dbinit"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"github.com/bpcoder16/pixiu/logit"
	"gorm.io/gorm"
)

// Store 借用命名数据库实例，从库扫描待发任务，主库处理发送和写入。
type Store struct {
	cfg        StoreConfig
	table      string
	masterPool *sql.DB
}

// Open 校验主库并在迁移锁内准备任务表，成功后 Store 即可使用。
func Open(ctx context.Context, cfg StoreConfig) (*Store, error) {
	return open(ctx, cfg, true)
}

// OpenExisting 检查已迁移的任务表及其所需列，查询失败时返回数据库错误。
func OpenExisting(ctx context.Context, cfg StoreConfig) (*Store, error) {
	return open(ctx, cfg, false)
}

func open(ctx context.Context, cfg StoreConfig, migrate bool) (*Store, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("%w: database client required", outboxerr.ErrInvalidArgument)
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	s := &Store{cfg: cfg}
	table := s.tableModel()
	s.table = table.TableName()
	db, err := s.master(ctx)
	if err != nil {
		return nil, err
	}
	s.masterPool, err = db.DB()
	if err != nil {
		return nil, err
	}
	if !migrate {
		// 读取模型的全部列并限制为零行，空表可启动，缺列在装配阶段报错。
		if err = db.Session(&gorm.Session{QueryFields: true}).Limit(0).Find(table).Error; err != nil {
			return nil, fmt.Errorf("outbox: read existing table %s: %w", s.table, err)
		}
		return s, nil
	}
	err = dbinit.Migrate(ctx, db, cfg.Backend, s.table, []schemamigrate.Table{
		{Model: table, Comment: "事务发件箱"},
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// tableSession 保留连接或事务及其 context，清除已有查询条件并指定任务表。
func tableSession(db *gorm.DB, table string) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Table(table)
}

func (s *Store) master(ctx context.Context) (*gorm.DB, error) {
	return s.session(ctx, DBClient.MasterDB)
}

func (s *Store) session(ctx context.Context, connect func(DBClient, context.Context) *gorm.DB) (*gorm.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", outboxerr.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.cfg.Client == nil {
		return nil, ErrInvalidSession
	}
	db := connect(s.cfg.Client, ctx)
	if err := dbinit.ValidateSession(db, s.cfg.Backend, false); err != nil {
		return nil, err
	}
	return tableSession(db, s.table), nil
}

// InsertTx 使用 Store 主库连接池开启的业务事务登记任务。最外层提交后可凭 ID 发送。
func (s *Store) InsertTx(tx *gorm.DB, task model.Task) (int64, error) {
	if err := dbinit.ValidateSession(tx, s.cfg.Backend, true); err != nil {
		return 0, err
	}
	// 任务 ID 只在本表唯一，其他命名实例的同号任务不能交给本 Store 发送。
	pool, err := tx.DB()
	if err != nil {
		return 0, errors.Join(ErrInvalidSession, err)
	}
	if pool != s.masterPool {
		return 0, fmt.Errorf("%w: transaction belongs to another master pool", ErrInvalidSession)
	}
	if !task.HandlerType.Valid() {
		return 0, fmt.Errorf("%w: invalid handler type %q", outboxerr.ErrInvalidArgument, task.HandlerType)
	}
	if len(task.Payload) > 1<<20 {
		return 0, fmt.Errorf("%w: payload exceeds 1 MiB", outboxerr.ErrInvalidArgument)
	}
	var expiresAt *time.Time
	if !task.ExpiresAt.IsZero() {
		value := task.ExpiresAt.In(s.cfg.Location).Truncate(time.Microsecond)
		if s.cfg.Backend == constant.MySQL && (value.Unix() < 1 || value.Unix() > math.MaxInt32) {
			return 0, fmt.Errorf("%w: expiration exceeds MySQL TIMESTAMP range", outboxerr.ErrInvalidArgument)
		}
		expiresAt = &value
	}
	logID, _ := logit.LogIDFromContext(tx.Statement.Context)
	// logId 仅用于调试。MySQL TEXT 最多容纳 65535 字节，PostgreSQL text 要求合法 UTF-8 且无零字节。
	if len(logID) > math.MaxUint16 || strings.IndexByte(logID, 0) >= 0 || !utf8.ValidString(logID) {
		logID = ""
	}
	now := time.Now().Truncate(time.Microsecond)
	record := model.Record{
		HandlerType:  task.HandlerType,
		ResourceName: task.ResourceName,
		Payload:      task.Payload,
		ExpiresAt:    expiresAt,
		Status:       constant.OutboxPending,
		LogID:        logID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	db := tableSession(tx, s.table)
	if s.cfg.Backend == constant.PostgreSQL {
		return createRecord[pgoutbox.Record](db, record)
	}
	return createRecord[mysqloutbox.Record](db, record)
}

// 用方言实体写入，使列定义和自增 ID 回填与迁表模型一致。
func createRecord[T recordEntity](db *gorm.DB, record model.Record) (int64, error) {
	row := T(record)
	if err := db.Create(&row).Error; err != nil {
		return 0, err
	}
	id := model.Record(row).ID
	if id <= 0 {
		return 0, errors.New("outbox: missing generated task ID")
	}
	return id, nil
}

// takeRecord 读取任务快照，并按 Store 配置转换时间字段。
func (s *Store) takeRecord(query *gorm.DB) (model.Record, error) {
	if s.cfg.Backend == constant.PostgreSQL {
		return takeRecord[pgoutbox.Record](query, s.cfg.Location)
	}
	return takeRecord[mysqloutbox.Record](query, s.cfg.Location)
}

func takeRecord[T recordEntity](query *gorm.DB, location *time.Location) (model.Record, error) {
	var row T
	if err := query.Take(&row).Error; err != nil {
		return model.Record{}, err
	}
	record := model.Record(row)
	for _, value := range []*time.Time{&record.CreatedAt, &record.UpdatedAt, record.ExpiresAt, record.FinishedAt} {
		if value != nil {
			*value = value.In(location)
		}
	}
	return record, nil
}
