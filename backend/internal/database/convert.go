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
