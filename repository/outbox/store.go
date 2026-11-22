// Package outbox 负责事务发件箱的表初始化、事务写入与持锁更新。
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
	model "github.com/bpcoder16/baize/model/outbox"
	"github.com/bpcoder16/baize/repository/internal/dbinit"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"github.com/bpcoder16/pixiu/logit"
	"gorm.io/gorm"
)

// Store 借用配置中的主库，由 Open 完成表结构迁移。
type Store struct {
	cfg        StoreConfig
	masterPool *sql.DB
}

// Open 校验主库并在迁移锁内准备任务表，成功后 Store 即可使用。
func Open(ctx context.Context, cfg StoreConfig) (*Store, error) {
	if cfg.MasterDB == nil {
		return nil, fmt.Errorf("%w: master database required", outboxerr.ErrInvalidArgument)
	}
	if err := dbinit.ValidateSession(cfg.MasterDB, cfg.Backend, false); err != nil {
		return nil, err
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	pool, err := cfg.MasterDB.DB()
	if err != nil {
		return nil, err
	}
	s := &Store{
		cfg:        cfg,
		masterPool: pool,
	}
	// 保存独立的基础会话，避免保留调用方的查询条件和 context。
	s.cfg.MasterDB = s.clean(cfg.MasterDB).WithContext(context.Background())
	db, err := s.master(ctx)
	if err != nil {
		return nil, err
	}
	table := s.tableModel()
	err = dbinit.Migrate(ctx, db, cfg.Backend, table.TableName(), []schemamigrate.Table{
		{Model: table, Comment: "事务发件箱"},
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// clean 保留原事务，清除调用方的查询条件，再绑定 Outbox 模型。
func (s *Store) clean(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Model(s.tableModel())
}

func (s *Store) master(ctx context.Context) (*gorm.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", outboxerr.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.cfg.MasterDB.WithContext(ctx), nil
}

// nowSQL 返回当前写入语句开始时的数据库时间表达式。
func (s *Store) nowSQL() string {
	if s.cfg.Backend == constant.MySQL {
		return "CURRENT_TIMESTAMP(6)"
	}
	return "statement_timestamp()"
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
	var expiresAt any
	if !task.ExpiresAt.IsZero() {
		value := task.ExpiresAt.In(s.cfg.Location).Truncate(time.Microsecond)
		if s.cfg.Backend == constant.MySQL && (value.Unix() < 1 || value.Unix() > math.MaxInt32) {
			return 0, fmt.Errorf("%w: expiration exceeds MySQL TIMESTAMP range", outboxerr.ErrInvalidArgument)
		}
		expiresAt = value
	}
	logID, _ := logit.LogIDFromContext(tx.Statement.Context)
	// logId 仅用于调试。MySQL TEXT 最多容纳 65535 字节，PostgreSQL text 要求合法 UTF-8 且无零字节。
	if len(logID) > math.MaxUint16 || strings.IndexByte(logID, 0) >= 0 || !utf8.ValidString(logID) {
		logID = ""
	}
	values := map[string]any{
		"handler_type":  task.HandlerType,
		"resource_name": task.ResourceName,
		"payload":       task.Payload,
		"expires_at":    expiresAt,
		"status":        constant.OutboxPending,
		"attempts":      int64(0),
		"last_error":    "",
		"log_id":        logID,
		"created_at":    gorm.Expr(s.nowSQL()),
		"updated_at":    gorm.Expr(s.nowSQL()),
	}
	// Model 提供自增字段元信息，GORM 通过 LastInsertId / RETURNING 将 ID 回填到 map。
	if err := s.clean(tx).Create(values).Error; err != nil {
		return 0, err
	}
	id, ok := values["id"].(int64)
	if !ok || id <= 0 {
		return 0, errors.New("outbox: missing generated task ID")
	}
	return id, nil
}

// takeRecord 读取任务快照，并按 Store 配置转换时间字段。
func (s *Store) takeRecord(query *gorm.DB) (model.Record, error) {
	var row model.Record
	if err := query.Take(&row).Error; err != nil {
		return model.Record{}, err
	}
	for _, value := range []*time.Time{&row.CreatedAt, &row.UpdatedAt, row.ExpiresAt, row.FinishedAt} {
		if value != nil {
			*value = value.In(s.cfg.Location)
		}
	}
	return row, nil
}
