package outbox

import (
	"errors"

	outboxerr "github.com/bpcoder16/baize/error/outbox"
	repo "github.com/bpcoder16/baize/repository/outbox"
)

var (
	ErrInvalidArgument      = outboxerr.ErrInvalidArgument
	ErrNotTransaction       = repo.ErrNotTransaction
	ErrBackendMismatch      = repo.ErrBackendMismatch
	ErrNotFound             = repo.ErrNotFound
	ErrBusy                 = repo.ErrBusy
	ErrInvalidResource      = outboxerr.ErrInvalidResource
	ErrInvalidSession       = repo.ErrInvalidSession
	ErrExpired              = errors.New("outbox: task expired")
	ErrHandlerNotRegistered = errors.New("outbox: handler not registered")
	ErrHandlerTypeMismatch  = errors.New("outbox: task handler type mismatch")
)
