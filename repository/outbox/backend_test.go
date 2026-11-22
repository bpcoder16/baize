package outbox

import (
	"errors"
	"fmt"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestBackendLockConflictAdapters(t *testing.T) {
	my := &mysqldriver.MySQLError{Number: 3572}
	pg := &pgconn.PgError{Code: "55P03"}
	for _, wrap := range []func(error) error{
		func(err error) error { return err },
		func(err error) error { return fmt.Errorf("数据库操作失败: %w", err) },
		func(err error) error { return errors.Join(errors.New("其他错误"), err) },
	} {
		if !isMySQLLockConflict(wrap(my)) || !isPostgreSQLLockConflict(wrap(pg)) {
			t.Fatal("两种后端都必须识别原始、包装和合并的 NOWAIT 错误")
		}
		if isMySQLLockConflict(wrap(pg)) || isPostgreSQLLockConflict(wrap(my)) {
			t.Fatal("不同驱动的错误不能混用")
		}
	}
	for _, err := range []error{
		nil,
		errors.New("3572 55P03"),
		&mysqldriver.MySQLError{Number: 1213},
		&pgconn.PgError{Code: "40001"},
	} {
		if isMySQLLockConflict(err) || isPostgreSQLLockConflict(err) {
			t.Fatalf("非 NOWAIT 错误不能被当作忙碌跳过: %v", err)
		}
	}
}

func TestOpenRejectsMissingMaster(t *testing.T) {
	if s, err := Open(t.Context(), StoreConfig{}); s != nil || err == nil {
		t.Fatalf("缺少主库: %+v %v", s, err)
	}
}
