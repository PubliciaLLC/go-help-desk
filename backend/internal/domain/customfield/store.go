package customfield

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for custom field definitions, assignments, and values.
type Store interface {
	// Field definitions
	CreateFieldDef(ctx context.Context, def FieldDef) (FieldDef, error)
	ListFieldDefs(ctx context.Context) ([]FieldDef, error)
	GetFieldDef(ctx context.Context, id uuid.UUID) (FieldDef, error)
	UpdateFieldDef(ctx context.Context, def FieldDef) error

	// Assignments of field defs to CTI nodes
	CreateAssignment(ctx context.Context, a Assignment) (Assignment, error)
	ListAssignments(ctx context.Context, scopeType ScopeType, scopeID uuid.UUID) ([]Assignment, error)
	GetAssignment(ctx context.Context, id uuid.UUID) (Assignment, error)
	UpdateAssignment(ctx context.Context, a Assignment) error
	DeleteAssignment(ctx context.Context, id uuid.UUID) error

	// Field values on tickets
	UpsertValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error
	DeleteValue(ctx context.Context, ticketID, fieldDefID uuid.UUID) error
	ListValuesForTicket(ctx context.Context, ticketID uuid.UUID) ([]TicketFieldValue, error)
}
