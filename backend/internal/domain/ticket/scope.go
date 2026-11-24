package ticket

import (
	"strings"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Scope is the `scope` a listing request names: which queue of tickets the
// caller wants. It is turned into a Queue by ResolveQueue, which both REST and
// MCP call, so the two answer the same way (#676).
type Scope string

const (
	ScopeMine         Scope = "mine"
	ScopeAssignedToMe Scope = "assigned_to_me"
	ScopeMyGroups     Scope = "my_groups"
	ScopeUnassigned   Scope = "unassigned"
	ScopeAll          Scope = "all"
	ScopeVisible      Scope = "visible"
)

// scopeRule is what one scope means. A table, so a seventh scope is a row and
// not another branch.
type scopeRule struct {
	queue Queue
	// yieldsToNamedSet: a request that names which tickets it wants
	// (reporter_id, requester, assignee_group_id) is not on a queue. This is
	// the 1.2 rule and applies to the four older values only; the two new ones
	// are always applied, so a group picked inside My Group's Tickets narrows
	// it and never widens it to a group the caller is not in.
	yieldsToNamedSet bool
	adminOnly        bool // refused to every role but admin
	needsAvailable   bool // refused unless admin or the Available setting allows the role
	// anyWhenScoped: under VisibilityScoped the queue is "everything the scope
	// admits", because an unassigned ticket in a covered category must not be
	// invisible to the people who should pick it up.
	anyWhenScoped bool
}

var scopeRules = map[Scope]scopeRule{
	ScopeMine:         {queue: QueueMine, yieldsToNamedSet: true, anyWhenScoped: true},
	ScopeAssignedToMe: {queue: QueueAssignedToMe},
	ScopeMyGroups:     {queue: QueueMyGroups},
	ScopeUnassigned:   {queue: QueueUnassigned, yieldsToNamedSet: true, needsAvailable: true},
	ScopeAll:          {queue: QueueAny, yieldsToNamedSet: true, adminOnly: true},
	ScopeVisible:      {queue: QueueAny, yieldsToNamedSet: true},
}

// ParseScope trims s and reports which scope it names. An empty or unknown
// value is not ok; what that means is the caller's rule (REST: mine, MCP:
// refused).
func ParseScope(s string) (Scope, bool) {
	sc := Scope(strings.TrimSpace(s))
	_, ok := scopeRules[sc]
	if !ok {
		return "", false
	}
	return sc, true
}

// ResolveQueue is the queue a scope names for a caller of this role. Call it
// after f.Visibility is set. availableOK is whether the Available queue is
// open to the role (admin.Service.AvailableQueueAllowed).
//
// A requester has no queues: their tickets are the ones they reported, which
// visibility already says. An unknown scope is mine, as it always was.
func ResolveQueue(scope Scope, f Filter, role user.Role, availableOK bool) (Queue, error) {
	r, ok := scopeRules[scope]
	if !ok {
		r = scopeRules[ScopeMine]
	}

	namesWho := f.ReporterID != nil || f.Requester != "" || f.AssigneeGroupID != nil
	if r.yieldsToNamedSet && namesWho {
		return QueueAny, nil
	}

	if r.adminOnly && role != user.RoleAdmin ||
		r.needsAvailable && role != user.RoleAdmin && !availableOK {
		return 0, &FilterError{Kind: FilterForbidden, Msg: "only admins can use this scope"}
	}

	if f.Visibility == VisibilityReporter {
		return QueueAny, nil
	}
	if r.anyWhenScoped && f.Visibility == VisibilityScoped {
		return QueueAny, nil
	}
	return r.queue, nil
}
