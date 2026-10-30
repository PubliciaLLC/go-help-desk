// Package database provides shared type-conversion helpers between dbgen
// (sqlc-generated) types and domain types. The helpers live here so each
// store package does not repeat the same null-unwrapping boilerplate.
package database

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// NullUUID wraps a *uuid.UUID into the nullable type sqlc/pgx expects.
func NullUUID(p *uuid.UUID) uuid.NullUUID {
	if p == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *p, Valid: true}
}

// UUIDPtr unwraps a nullable UUID to a pointer; nil when not valid.
func UUIDPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	v := n.UUID
	return &v
}

// NullString wraps a *string for nullable TEXT columns.
func NullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

// StringPtr unwraps a nullable string; nil when not valid.
func StringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// NullBool wraps a *bool for nullable BOOLEAN columns.
func NullBool(p *bool) sql.NullBool {
	if p == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *p, Valid: true}
}

// BoolPtr unwraps a nullable bool; nil when not valid.
func BoolPtr(n sql.NullBool) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Bool
	return &v
}

// NullTime wraps a *time.Time for nullable TIMESTAMPTZ columns.
func NullTime(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}

// TimePtr unwraps a nullable time; nil when not valid.
func TimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

// NullInt64 wraps a *int64 for nullable BIGINT columns.
func NullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

// Int64Ptr unwraps a nullable int64; nil when not valid.
func Int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
