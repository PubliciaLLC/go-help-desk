package group

import (
	"github.com/google/uuid"
)

// Group is a named pool of staff members that can be assigned tickets.
type Group struct {
	ID          uuid.UUID
	Name        string
	Description string
}

// GroupScope defines which Category/Type combinations a group handles.
// TypeID nil means the group is responsible for all Types under CategoryID.
// Items are intentionally excluded — scope is Category+Type only.
type GroupScope struct {
	GroupID    uuid.UUID
	CategoryID uuid.UUID
	TypeID     *uuid.UUID // nil = all Types under CategoryID
}

// IsInScope returns true if a ticket with the given CTI falls within this
// scope entry. A nil TypeID on the scope matches any typeID (including nil).
func (s GroupScope) IsInScope(categoryID uuid.UUID, typeID *uuid.UUID) bool {
	if s.CategoryID != categoryID {
		return false
	}
	if s.TypeID == nil {
		return true // matches all types
	}
	if typeID == nil {
		return false // scope requires a specific type but ticket has none
	}
	return *s.TypeID == *typeID
}
