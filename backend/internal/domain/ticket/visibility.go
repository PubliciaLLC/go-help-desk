package ticket

import (
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// StaffScope is what a staff member's group membership grants them: the groups
// they belong to, and the Category/Type pairs those groups cover.
//
// DESIGN.md, "Groups & Scope": scope is derived exclusively from group
// membership — there is no direct category assignment to an individual.
type StaffScope struct {
	// GroupIDs are the groups this person belongs to.
	GroupIDs []uuid.UUID

	// Scopes are the Category/Type pairs those groups cover. A nil TypeID is a
	// category-level scope covering every type beneath it.
	Scopes []ScopeRule
}

// ScopeRule is one Category/Type pair a group handles.
type ScopeRule struct {
	CategoryID uuid.UUID
	TypeID     *uuid.UUID // nil means the whole category
}

// covers reports whether this rule admits a ticket's CTI.
//
// Items deliberately do not factor in: DESIGN.md says staff in-scope for a Type
// see every Item under it.
func (r ScopeRule) covers(categoryID uuid.UUID, typeID *uuid.UUID) bool {
	if r.CategoryID != categoryID {
		return false
	}
	if r.TypeID == nil {
		return true // category-level scope covers every type
	}
	return typeID != nil && *r.TypeID == *typeID
}

// CanView reports whether an actor may see a ticket.
//
// DESIGN.md describes staff visibility twice and not identically — "tickets
// within their scope" in the roles table, "assigned to them and their groups"
// in the search section. This implements the union, which is the only reading
// that satisfies both: a ticket in your area that nobody has picked up yet is
// visible, and so is one assigned to you outside your usual area.
//
// Enforcement is opt-in per instance; when it is off the caller does not
// consult this at all and staff see everything, which is how every release
// before this behaved.
func CanView(t Ticket, actor Actor, scope StaffScope) bool {
	switch actor.Role {
	case user.RoleAdmin:
		return true

	case user.RoleUser:
		// Unchanged: a reporting user sees only what they reported.
		return actor.UserID != nil && t.ReporterUserID != nil && *t.ReporterUserID == *actor.UserID

	case user.RoleStaff:
		if actor.UserID == nil {
			return false
		}
		// Reported by them.
		if t.ReporterUserID != nil && *t.ReporterUserID == *actor.UserID {
			return true
		}
		// Assigned to them directly.
		if t.AssigneeUserID != nil && *t.AssigneeUserID == *actor.UserID {
			return true
		}
		// Assigned to one of their groups.
		if t.AssigneeGroupID != nil {
			for _, gid := range scope.GroupIDs {
				if gid == *t.AssigneeGroupID {
					return true
				}
			}
		}
		// Falls inside a Category/Type their groups cover.
		for _, rule := range scope.Scopes {
			if rule.covers(t.CategoryID, t.TypeID) {
				return true
			}
		}
		return false

	default:
		return false
	}
}
