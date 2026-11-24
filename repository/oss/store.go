// Package oss 读写上传记录，写入和迁移使用主库。
package oss

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bpcoder16/baize/constant"
	mysqloss "github.com/bpcoder16/baize/model/entity/mysql/oss"
	pgoss "github.com/bpcoder16/baize/model/entity/postgresql/oss"
	model "github.com/bpcoder16/baize/model/oss"
	"github.com/bpcoder16/baize/repository/internal/dbinit"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var ErrStateChanged = errors.New("OSS upload state changed")

// DBClient 提供绑定了调用方 context 的主、从库会话。
// 未配置从库时由客户端回退主库。
type DBClient interface {
	MasterDB(context.Context) *gorm.DB
	SlaveDB(context.Context) *gorm.DB
}

// StoreConfig 选择上传表所在的数据库，连接池由宿主管理。
type StoreConfig struct {
	Backend constant.Backend
	Client  DBClient
}

type Store struct {
	client  DBClient
	backend constant.Backend
	table   string
}

// Open 在命名迁移锁内准备上传表，成功后即可读写。
func Open(ctx context.Context, cfg StoreConfig) (*Store, error) {
	return open(ctx, cfg, true)
}

// OpenExisting 以零行查询检查上传记录所需的列，供关闭迁移的副本使用。
func OpenExisting(ctx context.Context, cfg StoreConfig) (*Store, error) {
	return open(ctx, cfg, false)
}

func open(ctx context.Context, cfg StoreConfig, migrate bool) (*Store, error) {
	if cfg.Client == nil {
		return nil, dbinit.ErrInvalidSession
	}
	master := cfg.Client.MasterDB(ctx)
	if err := dbinit.ValidateSession(master, cfg.Backend, false); err != nil {
		return nil, err
	}
	var table schema.Tabler = &mysqloss.Record{}
	if cfg.Backend == constant.PostgreSQL {
		table = &pgoss.Record{}
	}
	name := table.TableName()
	db := tableSession(master, name)
	if migrate {
		if err := dbinit.Migrate(ctx, db, cfg.Backend, name, []schemamigrate.Table{{Model: table, Comment: "OSS上传记录"}}); err != nil {
			return nil, err
		}
	} else if err := db.Session(&gorm.Session{QueryFields: true}).Limit(0).Find(table).Error; err != nil {
		return nil, fmt.Errorf("oss: read existing table %s: %w", name, err)
	}
	return &Store{client: cfg.Client, backend: cfg.Backend, table: name}, nil
}

// 清除客户端会话可能携带的查询条件，并指定上传记录表。
func tableSession(db *gorm.DB, table string) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Table(table)
}

// Create 批量登记申请或已直传的记录，并在切片中回填 ID 和应用服务器时间。
func (r *Store) Create(ctx context.Context, records []model.Record) error {
	if len(records) == 0 {
		return gorm.ErrEmptySlice
	}
	db := tableSession(r.client.MasterDB(ctx), r.table)
	now := time.Now().Truncate(time.Microsecond)
	if r.backend == constant.PostgreSQL {
		return createRecords[pgoss.Record](db, records, now)
	}
	return createRecords[mysqloss.Record](db, records, now)
}

type recordEntity interface {
	mysqloss.Record | pgoss.Record
}

// 用方言实体读写，使列名和默认值与迁表一致。批量插入成功后回填主键和时间。
func createRecords[T recordEntity](db *gorm.DB, records []model.Record, now time.Time) error {
	rows := make([]T, len(records))
	for i, record := range records {
		record.CreatedAt = now
		record.UpdatedAt = now
		record.ConfirmedAt = nil
		if record.Status == model.Success {
			record.ConfirmedAt = new(now)
		}
		rows[i] = T(record)
	}
	if err := db.Create(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		records[i] = model.Record(rows[i])
	}
	return nil
}

// Find 按 ID 用客户端的从库会话查询上传记录。查不到的 ID 省略。
func (r *Store) Find(ctx context.Context, ids []uint64) ([]model.Record, error) {
	return r.find(ctx, ids, r.client.SlaveDB)
}

// FindOnMaster 按 ID 从主库读取上传记录。查不到的 ID 省略。
func (r *Store) FindOnMaster(ctx context.Context, ids []uint64) ([]model.Record, error) {
	return r.find(ctx, ids, r.client.MasterDB)
}

func (r *Store) find(ctx context.Context, ids []uint64, session func(context.Context) *gorm.DB) ([]model.Record, error) {
	if len(ids) == 0 {
		return []model.Record{}, nil
	}
	db := session(ctx)
	if db == nil {
		return nil, errors.New("oss: read database session required")
	}
	query := tableSession(db, r.table).Where("id IN ?", ids)
	if r.backend == constant.PostgreSQL {
		return findRecords[pgoss.Record](query)
	}
	return findRecords[mysqloss.Record](query)
}

func findRecords[T recordEntity](query *gorm.DB) ([]model.Record, error) {
	var rows []T
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	records := make([]model.Record, len(rows))
	for i := range rows {
		records[i] = model.Record(rows[i])
	}
	return records, nil
}

// Confirm 将已核对的记录整批标为成功，任一记录在核对后发生变化则回滚。
func (r *Store) Confirm(ctx context.Context, records []model.Record) error {
	// 重叠批次按相同顺序获取行锁。仅在需要重排时复制，保留调用方的记录顺序。
	byID := func(a, b model.Record) int { return cmp.Compare(a.ID, b.ID) }
	if !slices.IsSortedFunc(records, byID) {
		records = slices.Clone(records)
		slices.SortFunc(records, byID)
	}
	db := tableSession(r.client.MasterDB(ctx), r.table)
	return db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().Truncate(time.Microsecond)
		for _, record := range records {
			// 状态条件防止并发重复确认；任何一条冲突时整批回滚。
			result := tx.Where("id = ? AND status = ?", record.ID, model.Pending).
				Where("object_key = ? AND bucket_type = ?", record.ObjectKey, record.BucketType).
				Updates(map[string]any{
					"status":           model.Success,
					"actual_file_size": record.ActualFileSize,
					"confirmed_at":     now,
					"updated_at":       now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrStateChanged
			}
		}
		return nil
	})
}
