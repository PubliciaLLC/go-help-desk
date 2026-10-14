package customfield

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// FieldType is the input type for a custom field.
type FieldType string

const (
	FieldTypeText     FieldType = "text"
	FieldTypeTextarea FieldType = "textarea"
	FieldTypeNumber   FieldType = "number"
	FieldTypeSelect   FieldType = "select"
)

// ScopeType indicates which CTI level a field assignment is attached to.
type ScopeType string

const (
	CategoryScope ScopeType = "category"
	TypeScope     ScopeType = "type"
	ItemScope     ScopeType = "item"
)

// FieldDef is a globally-defined custom field that can be assigned to CTI nodes.
type FieldDef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	FieldType FieldType `json:"field_type"`
	Options   []string  `json:"options,omitempty"` // populated only for select fields
	SortOrder int       `json:"sort_order"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Assignment attaches a FieldDef to a specific CTI node.
type Assignment struct {
	ID            uuid.UUID `json:"id"`
	FieldDefID    uuid.UUID `json:"field_def_id"`
	FieldDef      *FieldDef `json:"field_def,omitempty"` // populated on list responses
	ScopeType     ScopeType `json:"scope_type"`
	ScopeID       uuid.UUID `json:"scope_id"`
	SortOrder     int       `json:"sort_order"`
	VisibleOnNew  bool      `json:"visible_on_new"`
	RequiredOnNew bool      `json:"required_on_new"`
}

// TicketFieldValue holds the value of a custom field on a specific ticket.
type TicketFieldValue struct {
	TicketID   uuid.UUID `json:"ticket_id"`
	FieldDefID uuid.UUID `json:"field_def_id"`
	FieldName  string    `json:"field_name"`
	FieldType  FieldType `json:"field_type"`
	Options    []string  `json:"options,omitempty"`
	Value      string    `json:"value"`
	UpdatedAt  time.Time `json:"updated_at"`
}

var (
	// ErrNotFound is returned when a custom field assignment is not found.
	ErrNotFound = errors.New("not found")
	// ErrValidation is the caller's input being wrong: 400.
	ErrValidation = errors.New("invalid custom field")
	// ErrConflict is a duplicate name or a field assigned twice to one place:
	// 409.
	ErrConflict = errors.New("custom field conflict")
	// ErrInvalidReference is an id that names nothing: 400 invalid_reference.
	ErrInvalidReference = errors.New("custom field reference")
)

// kindError carries a message written for the caller and answers errors.Is for
// one of the sentinels above. The message is the whole text a client sees, so
// it can never be a database error: the store writes these from the SQLSTATE,
// not from the driver's text.
type kindError struct {
	kind error
	msg  string
}

func (e kindError) Error() string        { return e.msg }
func (e kindError) Is(target error) bool { return target == e.kind }

// Invalid, Conflict and InvalidReference build the three caller-fault errors.
func Invalid(msg string) error          { return kindError{ErrValidation, msg} }
func Conflict(msg string) error         { return kindError{ErrConflict, msg} }
func InvalidReference(msg string) error { return kindError{ErrInvalidReference, msg} }
