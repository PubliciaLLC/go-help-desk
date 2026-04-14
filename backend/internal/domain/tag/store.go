package tag

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for tags and ticket-tag associations.
type Store interface {
	// Tags
	Create(ctx context.Context, name string) (Tag, error)
	GetByName(ctx context.Context, name string) (Tag, error)
	ListActive(ctx context.Context) ([]Tag, error)
	ListAll(ctx context.Context) ([]Tag, error)
	Search(ctx context.Context, prefix string) ([]Tag, error)
	SoftDelete(ctx context.Context, id uuid.UUID) error
	Restore(ctx context.Context, id uuid.UUID) error

	// Ticket associations
	ListForTicket(ctx context.Context, ticketID uuid.UUID) ([]Tag, error)
	AddToTicket(ctx context.Context, ticketID, tagID uuid.UUID) error
	RemoveFromTicket(ctx context.Context, ticketID, tagID uuid.UUID) error
}
