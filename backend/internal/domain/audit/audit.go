package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Entry records a single mutation on any entity in the system.
type Entry struct {
	ID         uuid.UUID
	ActorID    *uuid.UUID     // nil for system-generated actions
	EntityType string         // "ticket", "user", "group", etc.
	EntityID   uuid.UUID
	Action     string         // "created", "status_changed", "assigned", etc.
	Before     map[string]any // nil for create actions
	After      map[string]any // nil for delete actions
	CreatedAt  time.Time
}

// Store persists audit entries. Implementations must not return errors for
// individual entry failures — log and continue rather than blocking the caller.
type Store interface {
	Create(ctx context.Context, e Entry) error
	ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]Entry, error)
}
