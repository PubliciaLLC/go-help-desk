// Package auditstore implements domain/audit.Store against PostgreSQL.
package auditstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/database"
	"github.com/open-help-desk/open-help-desk/backend/internal/dbgen"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/audit"
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
