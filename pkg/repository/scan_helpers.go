package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ScannableBool handles scanning booleans from both Postgres (bool) and SQLite (int64).
type ScannableBool struct {
	Val bool
}

func (s *ScannableBool) Scan(src interface{}) error {
	switch v := src.(type) {
	case bool:
		s.Val = v
	case int64:
		s.Val = v != 0
	case nil:
		s.Val = false
	default:
		return fmt.Errorf("ScannableBool: unsupported type %T", src)
	}
	return nil
}

// ScanUUID parses a UUID from a scanned string value.
func ScanUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

// ScanNullTime parses an optional time from a sql.NullString (for SQLite TEXT timestamps)
// or returns the value directly if it's already a time.Time (Postgres).
type ScannableTime struct {
	Val   *time.Time
}

func (s *ScannableTime) Scan(src interface{}) error {
	switch v := src.(type) {
	case time.Time:
		s.Val = &v
	case string:
		if v == "" {
			s.Val = nil
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			// Try date-only format
			t, err = time.Parse("2006-01-02", v)
			if err != nil {
				// Try SQLite datetime format
				t, err = time.Parse("2006-01-02 15:04:05", v)
				if err != nil {
					return fmt.Errorf("ScannableTime: cannot parse %q", v)
				}
			}
		}
		s.Val = &t
	case nil:
		s.Val = nil
	default:
		return fmt.Errorf("ScannableTime: unsupported type %T", src)
	}
	return nil
}

// NullString converts a sql.NullString to *string.
func NullString(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

// NullFloat converts a sql.NullFloat64 to *float64.
func NullFloat(nf sql.NullFloat64) *float64 {
	if nf.Valid {
		return &nf.Float64
	}
	return nil
}
