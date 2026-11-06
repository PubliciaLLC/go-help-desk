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

	// SetValues writes every entry or none, in one transaction. An empty value
	// deletes the row.
	//
	// It locks the definitions the entries name (FOR SHARE, in id order), passes
	// them to check, and writes only if check returns nil. The rule lives in
	// check, in this package; the store supplies what the rule cannot get for
	// itself: the definitions as they are while the lock is held. check can run
	// more than once, when the database aborts a transaction to break a lock
	// cycle and the whole thing is tried again, so it must not keep state. An
	// id that names no definition is simply absent from check's map.
	SetValues(ctx context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, check func(defs map[uuid.UUID]FieldDef) error) error
}
