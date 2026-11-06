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
	// RetiredOptions are options no longer offered that tickets may still hold.
	// A value is in Options or here, never both. The admin routes return it; the
	// ticket routes build their FieldDef without it, so they never do.
	RetiredOptions []string  `json:"retired_options,omitempty"`
	SortOrder      int       `json:"sort_order"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	// PlaceCount is how many places the field is attached to. Only
	// ListFieldDefs sets it (a pointer, so a field on no form says 0 rather than
	// leaving the key out).
	PlaceCount *int `json:"place_count,omitempty"`
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

// PlaceNode is one level of a Placement's path.
type PlaceNode struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

// Placement is an Assignment with the names of the category, type and item it
// is attached at, for the page that shows where a field appears. Type and Item
// are nil above their level.
type Placement struct {
	Assignment
	Category PlaceNode  `json:"category"`
	Type     *PlaceNode `json:"type"`
	Item     *PlaceNode `json:"item"`
}

// ValueCount is how many tickets hold one value of a select field.
type ValueCount struct {
	Value   string `json:"value"`
	Tickets int    `json:"tickets"`
}

// Usage is how many tickets hold a value for a field, and for a select field how
// many hold each value.
type Usage struct {
	TicketsWithValue int          `json:"tickets_with_value"`
	Values           []ValueCount `json:"values"`
}

// Reason is why a value for a field was not written. The same words are the
// reason codes in POST /tickets' warnings.
type Reason string

const (
	ReasonInvalidFieldID   Reason = "invalid_field_id"   // the key is not a UUID
	ReasonDuplicateFieldID Reason = "duplicate_field_id" // two keys spell the same field id; neither is written
	ReasonNotOnTicket      Reason = "not_on_ticket"      // not on the ticket's category, type or item
	ReasonInactive         Reason = "inactive"
	ReasonRetiredOption    Reason = "retired_option"
	ReasonNotAnOption      Reason = "not_an_option"
	ReasonNotANumber       Reason = "not_a_number"
	ReasonNotSaved         Reason = "not_saved" // anything else
)

// Warning is one value dropped when a ticket was created. FieldID is the key
// exactly as sent, which is why it is a string: for invalid_field_id it is not
// a UUID.
type Warning struct {
	FieldID string `json:"field_id"`
	Reason  Reason `json:"reason"`
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
