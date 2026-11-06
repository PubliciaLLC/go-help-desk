package customfield

import (
	"context"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Held answers, inside the transaction of an admin change to a definition,
// whether tickets hold values of that field. The definition's row is locked FOR
// UPDATE by then, and every value write holds it FOR SHARE, so the answer is
// still true when the change commits.
type Held interface {
	// Any reports whether any ticket holds a value for the field.
	Any() (bool, error)
	// OneOf returns a value from values that some ticket holds.
	OneOf(values []string) (value string, ok bool, err error)
}

// Store is the persistence interface for custom field definitions, assignments, and values.
//
// Every method that changes a definition or an attachment takes the audit
// entries for the change and writes them in the same transaction: a history
// entry committed apart from the change it describes is not a history. Where
// the entries depend on state the transaction reads (the row before it is
// changed, the field's name, the node's path), the method takes a function that
// builds them from it. The two creates take them like the rest; a
// fixture that wants only the row passes nil.
type Store interface {
	// Field definitions
	CreateFieldDef(ctx context.Context, def FieldDef, entries []audit.Entry) (FieldDef, error)
	// ListFieldDefs is every definition in field-number order with PlaceCount set.
	ListFieldDefs(ctx context.Context) ([]FieldDef, error)
	GetFieldDef(ctx context.Context, id uuid.UUID) (FieldDef, error)
	// UpdateFieldDef locks the definition FOR UPDATE, passes it to decide with a
	// way to ask what tickets hold, and writes what decide returns and its audit
	// entries, or nothing if decide returns an error. decide can run more than
	// once, when the database aborts a transaction to break a lock cycle and the
	// whole thing is tried again, so it must not keep state. The saved
	// definition is returned.
	UpdateFieldDef(ctx context.Context, id uuid.UUID, decide func(cur FieldDef, held Held) (FieldDef, []audit.Entry, error)) (FieldDef, error)
	// ReorderFieldDefs locks every definition FOR UPDATE and passes them to
	// decide, which returns the new order (position n becomes number n) and the
	// audit entries, or refuses it.
	ReorderFieldDefs(ctx context.Context, decide func(cur []FieldDef) ([]uuid.UUID, []audit.Entry, error)) error

	// Counts of the values tickets hold for a field, for the usage route.
	CountValues(ctx context.Context, fieldDefID uuid.UUID) (int, error)
	CountValuesByValue(ctx context.Context, fieldDefID uuid.UUID) (map[string]int, error)

	// Assignments of field defs to CTI nodes
	CreateAssignment(ctx context.Context, a Assignment, entry func(AssignmentInfo) audit.Entry) (Assignment, error)
	ListAssignments(ctx context.Context, scopeType ScopeType, scopeID uuid.UUID) ([]Assignment, error)
	// ListPlacements is where a field is attached, in tree order.
	ListPlacements(ctx context.Context, fieldDefID uuid.UUID) ([]Placement, error)
	GetAssignment(ctx context.Context, id uuid.UUID) (Assignment, error)
	// UpdateAssignment and DeleteAssignment pass entries the assignment as it
	// stood, read under a lock in the same transaction.
	UpdateAssignment(ctx context.Context, a Assignment, entries func(before Assignment, info AssignmentInfo) []audit.Entry) error
	DeleteAssignment(ctx context.Context, id uuid.UUID, entry func(before Assignment, info AssignmentInfo) audit.Entry) error

	// Field values on tickets
	UpsertValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error
	DeleteValue(ctx context.Context, ticketID, fieldDefID uuid.UUID) error
	ListValuesForTicket(ctx context.Context, ticketID uuid.UUID) ([]TicketFieldValue, error)

	// SetValues writes in one transaction. An empty value deletes the row.
	//
	// It locks the definitions the entries name (FOR SHARE, in id order), passes
	// them to decide, and writes what decide returns, which may be fewer entries
	// than were given (a best-effort caller drops the ones it refuses) or the
	// same entries rewritten (a number is stored trimmed), or nothing at all if
	// decide returns an error. The rule lives in decide, in this package; the
	// store supplies what the rule cannot get for itself: the definitions as
	// they are while the lock is held. decide can run more than once, when the
	// database aborts a transaction to break a lock cycle and the whole thing is
	// tried again, so it must not keep state. An id that names no definition is
	// simply absent from decide's map.
	SetValues(ctx context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, decide func(defs map[uuid.UUID]FieldDef) (map[uuid.UUID]string, error)) error
}
