package outbox

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func isMySQLLockConflict(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 3572
}
