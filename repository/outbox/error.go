package outbox

import (
	"errors"

	"github.com/bpcoder16/baize/repository/internal/dbinit"
)

var (
	ErrInvalidSession  = dbinit.ErrInvalidSession
	ErrNotTransaction  = dbinit.ErrNotTransaction
	ErrBackendMismatch = dbinit.ErrBackendMismatch
	ErrNotFound        = errors.New("outbox: task not found")
	ErrBusy            = errors.New("outbox: sender busy")
)
