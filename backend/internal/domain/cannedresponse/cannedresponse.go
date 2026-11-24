// Package cannedresponse holds the domain logic for reusable reply templates
// that staff insert into ticket replies. See docs/DESIGN.md → Canned Responses.
package cannedresponse

import (
	"time"

	"github.com/google/uuid"
)

// CannedResponse is a reusable reply template.
//
// Scope is expressed by CategoryID and TypeID:
//   - both nil           → global (available on every ticket)
//   - CategoryID set      → available on any ticket in that category
//   - CategoryID+TypeID   → available only on tickets matching that category and type
//
// A TypeID is only ever set when CategoryID is also set.
type CannedResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Body       string     `json:"body"`
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	TypeID     *uuid.UUID `json:"type_id,omitempty"`
	SortOrder  int        `json:"sort_order"`
	CreatedAt  time.Time  `json:"created_at"`
}

// available reports whether the response should surface for a ticket in the
// given category and (optional) type. This is the single source of truth for
// the reply-composer picker filter.
func available(cr CannedResponse, categoryID uuid.UUID, typeID *uuid.UUID) bool {
	if cr.CategoryID == nil {
		return true // global
	}
	if *cr.CategoryID != categoryID {
		return false
	}
	if cr.TypeID == nil {
		return true // whole category
	}
	// Type-scoped: the ticket must have a matching type.
	return typeID != nil && *cr.TypeID == *typeID
}
