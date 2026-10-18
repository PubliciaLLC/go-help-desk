package auditstore

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// ListByEntityActions is ListByEntity narrowed by action in the query, so what
// is left out never counts toward limit.
func (s *Store) ListByEntityActions(ctx context.Context, entityType string, entityID uuid.UUID, f audit.ActionFilter, limit, offset int) ([]audit.Entry, error) {
	rows, err := s.q.ListAuditByEntityActions(ctx, dbgen.ListAuditByEntityActionsParams{
		EntityType:     entityType,
		EntityID:       entityID,
		OnlyActions:    nilIfEmpty(f.Only),
		ExcludeActions: nilIfEmpty(f.Exclude),
		PageLimit:      int32(limit),
		PageOffset:     int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	out := make([]audit.Entry, len(rows))
	for i, r := range rows {
		out[i] = audit.Entry{
			ID: r.ID, ActorID: database.UUIDPtr(r.ActorID), EntityType: r.EntityType, EntityID: r.EntityID,
			Action: r.Action, Before: unmarshalMap(r.Before), After: unmarshalMap(r.After), CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
