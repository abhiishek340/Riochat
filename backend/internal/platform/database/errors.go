package database

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Generic conditions every feature area can test with errors.Is.
var (
	ErrNotFound  = errors.New("database: record not found")
	ErrDuplicate = errors.New("database: duplicate record")
)

// pgUniqueViolation is the PostgreSQL SQLSTATE for unique/primary-key conflicts.
const pgUniqueViolation = "23505"

// Translate maps driver errors to generic conditions: a missing row becomes
// ErrNotFound, unique and primary-key violations become ErrDuplicate, and any
// other error passes through unchanged. The original error stays in the chain.
func Translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrDuplicate):
		return err
	case errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case isUniqueViolation(err):
		return fmt.Errorf("%w: %w", ErrDuplicate, err)
	default:
		return err
	}
}

// IsNotFound reports a missing-row condition (translated or raw).
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound)
}

// IsDuplicate reports a unique/primary-key violation (translated or raw).
func IsDuplicate(err error) bool {
	return errors.Is(err, ErrDuplicate) || isUniqueViolation(err)
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return true
	}
	var sqErr sqlite3.Error
	if errors.As(err, &sqErr) {
		switch sqErr.ExtendedCode {
		case sqlite3.ErrConstraintUnique, sqlite3.ErrConstraintPrimaryKey, sqlite3.ErrConstraintRowID:
			return true
		}
	}
	return false
}
