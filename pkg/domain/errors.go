package domain

import "errors"

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("forbidden")
	ErrConflict         = errors.New("conflict")
	ErrBadRequest       = errors.New("bad request")
	ErrDuplicateReceipt = errors.New("duplicate receipt")
	// ErrUniqueViolation is returned when an insert races with another writer
	// and trips a UNIQUE index. Callers should re-lookup the existing row.
	ErrUniqueViolation = errors.New("unique constraint violation")
)
