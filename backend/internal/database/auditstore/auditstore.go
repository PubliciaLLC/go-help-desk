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

// Search reads the page and counts the matches, both from the same predicate.
// The page is fetched one row long: the extra row is how HasMore is known
// without consulting the count, which stops at audit.TotalCap. The count is
// asked for TotalCap+1 matches, so "more than the cap" is told apart from
// "exactly the cap" without counting any further.
func (s *Store) Search(ctx context.Context, f audit.Filter, limit, offset int) (audit.Page, error) {
	p := searchParams(f)
	rows, err := s.q.SearchAuditLog(ctx, dbgen.SearchAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		Q:          p.q,
		ScopedTo:   p.scopedTo,
		PageLimit:  int32(limit + 1),
		PageOffset: int32(offset),
	})
	if err != nil {
		return audit.Page{}, fmt.Errorf("searching audit entries: %w", err)
	}
	counted, err := s.q.CountAuditLog(ctx, dbgen.CountAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		Q:          p.q,
		ScopedTo:   p.scopedTo,
		CountCap:   audit.TotalCap + 1,
	})
	if err != nil {
		return audit.Page{}, fmt.Errorf("counting audit entries: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	capped := counted > audit.TotalCap
	total := int(counted)
	if capped {
		total = audit.TotalCap
	}
	return audit.Page{Entries: toEntries(rows), Total: total, TotalCapped: capped, HasMore: hasMore}, nil
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
	actorID, scopedTo     uuid.NullUUID
	from, to              sql.NullTime
}

func searchParams(f audit.Filter) searchParamValues {
	return searchParamValues{
		entityType: database.NullString(nonEmpty(f.EntityType)),
		action:     database.NullString(nonEmpty(f.Action)),
		q:          database.NullString(nonEmpty(f.Q)),
		actorID:    database.NullUUID(f.ActorID),
		scopedTo:   database.NullUUID(f.ScopedTo),
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
