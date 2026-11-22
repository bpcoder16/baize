package dbinit

import (
	"errors"
	"fmt"

	"github.com/bpcoder16/baize/constant"
	"gorm.io/gorm"
)

var (
	ErrInvalidSession  = errors.New("database: usable session required")
	ErrBackendMismatch = errors.New("database: backend mismatch")
	ErrNotTransaction  = errors.New("database: transaction required")
)

// ValidateSession 区分主库会话与业务事务，防止仓储在错误的事务边界上读写。
func ValidateSession(db *gorm.DB, backend constant.Backend, transaction bool) error {
	if db == nil || db.DryRun {
		return ErrInvalidSession
	}
	if db.Error != nil {
		return db.Error
	}
	var dialect string
	switch backend {
	case constant.MySQL:
		dialect = "mysql"
	case constant.PostgreSQL:
		dialect = "postgres"
	default:
		return fmt.Errorf("unsupported database backend %q", backend)
	}
	if db.Dialector.Name() != dialect {
		return ErrBackendMismatch
	}
	_, isTx := db.Statement.ConnPool.(gorm.TxCommitter)
	if transaction && !isTx {
		return ErrNotTransaction
	}
	if !transaction && isTx {
		return fmt.Errorf("%w: master database is a transaction", ErrInvalidSession)
	}
	return nil
}
