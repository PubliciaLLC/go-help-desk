package customfield

import (
	"encoding/json"
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

// The longest value a field takes, in characters (runes: a limit in bytes would
// treat a person writing in Chinese or with emoji as someone writing four times
// as much). Owner, 2026-11-11: single-line fields 255, long text 10,000.
const (
	MaxTextChars     = 255
	MaxLongTextChars = 10_000
)

// MaxChars is the longest value a field of the type takes: the long limit for
// textarea and the single-line limit for everything else. A number is a few
// dozen characters in any real use and a select value is one of the options,
// so neither needed a limit of its own.
func MaxChars(ft FieldType) int {
	if ft == FieldTypeTextarea {
		return MaxLongTextChars
	}
	return MaxTextChars
}

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
	// A value is in Options or here, never both. Every route that returns a
	// definition or a value's field returns it (the ticket routes since 1.3.0).
	RetiredOptions []string  `json:"retired_options,omitempty"`
	SortOrder      int       `json:"sort_order"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	// PlaceCount is how many places the field is attached to. Only
	// ListFieldDefs sets it (a pointer, so a field on no form says 0 rather than
	// leaving the key out).
	PlaceCount *int `json:"place_count,omitempty"`
	// MaxLength is the administrator's own limit on a value, lower than the
	// type's (MaxChars); nil means the type's applies. It is enforced by the API
	// (checkValue) and the forms, and by nothing below them: the store and the
	// database know only the type's limit.
	MaxLength *int `json:"max_length,omitempty"`
}

// EffectiveMaxLength is the longest value the API accepts for the field: the
// administrator's limit when there is one (it is always lower than the type's,
// which CreateFieldDefLimited and PatchFieldDef see to), else the type's.
func (d FieldDef) EffectiveMaxLength() int { return effectiveMax(d.FieldType, d.MaxLength) }

func effectiveMax(ft FieldType, own *int) int {
	if own != nil && *own > 0 && *own < MaxChars(ft) {
		return *own
	}
	return MaxChars(ft)
}

// MarshalJSON adds effective_max_length to every definition the API sends, so a
// form can set maxlength without knowing the type's limits: max_length is the
// administrator's own setting (absent when there is none), and this is what
// applies.
func (d FieldDef) MarshalJSON() ([]byte, error) {
	type plain FieldDef
	return json.Marshal(struct {
		plain
		EffectiveMaxLength int `json:"effective_max_length"`
	}{plain(d), d.EffectiveMaxLength()})
}

// MarshalJSON adds effective_max_length, for the edit box on the ticket page.
func (v TicketFieldValue) MarshalJSON() ([]byte, error) {
	type plain TicketFieldValue
	return json.Marshal(struct {
		plain
		EffectiveMaxLength int `json:"effective_max_length"`
	}{plain(v), effectiveMax(v.FieldType, v.MaxLength)})
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
	// VisibleToGuests offers the field on the guest form. See GuestFields.
	VisibleToGuests bool `json:"visible_to_guests"`
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
	ReasonTooLong          Reason = "too_long"  // over the longest value the field's type takes (MaxChars)
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
	// RetiredOptions lets a client show a held value that is no longer offered
	// as retired rather than as unknown.
	RetiredOptions []string `json:"retired_options,omitempty"`
	// MaxLength is the field's own limit, when it has one (FieldDef.MaxLength),
	// so a form can set maxlength on the edit box. Sent as effective_max_length.
	MaxLength *int      `json:"-"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
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
