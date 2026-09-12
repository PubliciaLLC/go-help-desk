package server

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// staffScopeFor resolves the groups an actor belongs to and the Category/Type
// pairs those groups cover.
//
// Two lookups per request. That is acceptable because it only runs when scope
// enforcement is switched on, and only for staff — admins short-circuit and
// reporting users are handled by the existing reporter check.
func (s *Server) staffScopeFor(r *http.Request, a *authmw.Actor) (ticket.StaffScope, error) {
	if a == nil || a.Role != user.RoleStaff {
		return ticket.StaffScope{}, nil
	}

	ctx := r.Context()
	groups, err := s.groups.ListGroupsForUser(ctx, a.UserID)
	if err != nil {
		return ticket.StaffScope{}, fmt.Errorf("listing groups for user: %w", err)
	}

	scope := ticket.StaffScope{GroupIDs: make([]uuid.UUID, 0, len(groups))}
	for _, g := range groups {
		scope.GroupIDs = append(scope.GroupIDs, g.ID)
		rules, err := s.groups.ListScopes(ctx, g.ID)
		if err != nil {
			return ticket.StaffScope{}, fmt.Errorf("listing scopes for group %s: %w", g.ID, err)
		}
		for _, sc := range rules {
			scope.Scopes = append(scope.Scopes, ticket.ScopeRule{
				CategoryID: sc.CategoryID,
				TypeID:     sc.TypeID,
			})
		}
	}
	return scope, nil
}

// canViewTicket reports whether the request's actor may see this ticket.
//
// When scope enforcement is off — the default, and how every release before
// this behaved — staff see everything and only the reporting-user restriction
// applies. That check lives here rather than at each call site so the two
// cannot drift apart, which is the mistake that left MCP unscoped while the
// REST API was not.
func (s *Server) canViewTicket(r *http.Request, t ticket.Ticket) (bool, error) {
	a := authmw.GetActor(r)
	if a == nil {
		return false, nil
	}

	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}

	if !s.adminSvc.TicketScopeEnforced(r.Context()) {
		// Legacy behaviour: admins and staff see everything, users see their own.
		if a.Role == user.RoleUser {
			return t.ReporterUserID != nil && *t.ReporterUserID == a.UserID, nil
		}
		return true, nil
	}

	scope, err := s.staffScopeFor(r, a)
	if err != nil {
		return false, err
	}
	return ticket.CanView(t, actor, scope), nil
}
