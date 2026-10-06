// Package auditstore implements domain/audit.Store against PostgreSQL.
package auditstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/sqlc-dev/pqtype"
)

// Store implements audit.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Create(ctx context.Context, e audit.Entry) error {
	before, _ := marshalMap(e.Before)
	after, _ := marshalMap(e.After)
	return s.q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
		ID:         e.ID,
		ActorID:    database.NullUUID(e.ActorID),
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Action:     e.Action,
		Before:     before,
		After:      after,
		CreatedAt:  time.Now(),
	})
}

func (s *Store) Search(ctx context.Context, f audit.Filter, limit, offset int) ([]audit.Entry, int, error) {
	out, err := s.List(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	params := searchParams(f)
	total, err := s.q.CountAuditLog(ctx, dbgen.CountAuditLogParams{
		EntityType: params.entityType,
		Action:     params.action,
		ActorID:    params.actorID,
		FromTs:     params.from,
		ToTs:       params.to,
		Q:          params.q,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("counting audit entries: %w", err)
	}
	return out, int(total), nil
}

func (s *Store) List(ctx context.Context, f audit.Filter, limit, offset int) ([]audit.Entry, error) {
	params := searchParams(f)
	rows, err := s.q.SearchAuditLog(ctx, dbgen.SearchAuditLogParams{
		EntityType: params.entityType,
		Action:     params.action,
		ActorID:    params.actorID,
		FromTs:     params.from,
		ToTs:       params.to,
		Q:          params.q,
		PageLimit:  int32(limit),
		PageOffset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("searching audit entries: %w", err)
	}
	return toEntries(rows), nil
}

func (s *Store) ListAfter(ctx context.Context, f audit.Filter, after *audit.Cursor, limit int) ([]audit.Entry, error) {
	params := searchParams(f)
	arg := dbgen.ListAuditLogAfterParams{
		EntityType: params.entityType,
		Action:     params.action,
		ActorID:    params.actorID,
		FromTs:     params.from,
		ToTs:       params.to,
		Q:          params.q,
		PageLimit:  int32(limit),
	}
	if after != nil {
		arg.AfterTs = sql.NullTime{Time: after.CreatedAt, Valid: true}
		arg.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	rows, err := s.q.ListAuditLogAfter(ctx, arg)
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	return toEntries(rows), nil
}

func toEntries(rows []dbgen.AuditLog) []audit.Entry {
	out := make([]audit.Entry, len(rows))
	for i, r := range rows {
		out[i] = audit.Entry{
			ID:         r.ID,
			ActorID:    database.UUIDPtr(r.ActorID),
			EntityType: r.EntityType,
			EntityID:   r.EntityID,
			Action:     r.Action,
			Before:     unmarshalMap(r.Before),
			After:      unmarshalMap(r.After),
			CreatedAt:  r.CreatedAt,
		}
	}
	return out
}

func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteAuditLogBefore(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("deleting expired audit entries: %w", err)
	}
	return n, nil
}

type searchParamValues struct {
	entityType, action, q sql.NullString
	actorID               uuid.NullUUID
	from, to              sql.NullTime
}

func searchParams(f audit.Filter) searchParamValues {
	return searchParamValues{
		entityType: database.NullString(nonEmpty(f.EntityType)),
		action:     database.NullString(nonEmpty(f.Action)),
		q:          database.NullString(nonEmpty(f.Q)),
		actorID:    database.NullUUID(f.ActorID),
		from:       database.NullTime(f.From),
		to:         database.NullTime(f.To),
	}
}

// nonEmpty turns an empty filter string into a nil pointer, so an unset
// EntityType/Action/Q reaches the query as SQL NULL — "match anything" —
// rather than as the empty string, which would only ever match a row that
// literally has one.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Store) ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]audit.Entry, error) {
	rows, err := s.q.ListAuditByEntity(ctx, dbgen.ListAuditByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
		Limit:      int32(limit),
		Offset:     int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	out := make([]audit.Entry, len(rows))
	for i, r := range rows {
		out[i] = audit.Entry{
			ID:         r.ID,
			ActorID:    database.UUIDPtr(r.ActorID),
			EntityType: r.EntityType,
			EntityID:   r.EntityID,
			Action:     r.Action,
			Before:     unmarshalMap(r.Before),
			After:      unmarshalMap(r.After),
			CreatedAt:  r.CreatedAt,
		}
	}
	return out, nil
}

func marshalMap(m map[string]any) (pqtype.NullRawMessage, error) {
	if m == nil {
		return pqtype.NullRawMessage{}, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return pqtype.NullRawMessage{}, err
	}
	return pqtype.NullRawMessage{RawMessage: b, Valid: true}, nil
}

func unmarshalMap(n pqtype.NullRawMessage) map[string]any {
	if !n.Valid {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(n.RawMessage, &m)
	return m
}
