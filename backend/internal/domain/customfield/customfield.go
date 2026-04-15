package customfield

import (
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
	ID             uuid.UUID  `json:"id"`
	FieldDefID     uuid.UUID  `json:"field_def_id"`
	FieldDef       *FieldDef  `json:"field_def,omitempty"` // populated on list responses
	ScopeType      ScopeType  `json:"scope_type"`
	ScopeID        uuid.UUID  `json:"scope_id"`
	SortOrder      int        `json:"sort_order"`
	VisibleOnNew   bool       `json:"visible_on_new"`
	RequiredOnNew  bool       `json:"required_on_new"`
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
