// Package dbinit 校验业务仓储的数据库会话，并在命名锁内执行表迁移。
package dbinit

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/bpcoder16/baize/constant"
	"github.com/bpcoder16/pixiu/infra/schemamigrate"
	"gorm.io/gorm"
)

// Migrate 在同一物理连接持有数据库会话锁，同一库的各个副本须使用相同的迁移名称。
func Migrate(ctx context.Context, db *gorm.DB, backend constant.Backend, name string, tables []schemamigrate.Table) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) (err error) {
		// 每条查询使用独立 Statement，共享同一个持锁的物理连接。
		conn = conn.Session(&gorm.Session{NewDB: true})
		lockSQL := "SELECT pg_advisory_lock(hashtext(current_schema()), hashtext(?))"
		unlockSQL := "SELECT pg_advisory_unlock(hashtext(current_schema()), hashtext(?))"
		if backend == constant.MySQL {
			lockSQL = "SELECT GET_LOCK(SHA2(CONCAT(DATABASE(), ':', ?), 256), 86400)"
			unlockSQL = "SELECT RELEASE_LOCK(SHA2(CONCAT(DATABASE(), ':', ?), 256))"
		}
		// 超时或网络错误时加锁结果可能未知，不能把可能仍持锁的会话还给池。
		discard := func() { _ = conn.Statement.ConnPool.(*sql.Conn).Raw(func(any) error { return driver.ErrBadConn }) }
		if backend == constant.MySQL {
			var acquired int
			if lockErr := conn.Raw(lockSQL, name).Row().Scan(&acquired); lockErr != nil {
				discard()
				return lockErr
			}
			if acquired != 1 {
				return fmt.Errorf("migration lock %q unavailable", name)
			}
		} else if lockErr := conn.Exec(lockSQL, name).Error; lockErr != nil {
			discard()
			return lockErr
		}
		defer func() {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			var released bool
			unlockErr := conn.WithContext(release).Raw(unlockSQL, name).Row().Scan(&released)
			if unlockErr != nil || !released {
				discard()
				err = errors.Join(err, fmt.Errorf("could not release migration lock %q", name), unlockErr)
			}
		}()
		_, err = schemamigrate.Apply(ctx, conn.WithContext, tables)
		return err
	})
}
