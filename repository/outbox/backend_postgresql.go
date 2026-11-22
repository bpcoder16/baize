package outbox

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

func isPostgreSQLLockConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}
