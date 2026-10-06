package server

import (
	"context"
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
func (s *Server) staffScopeFor(ctx context.Context, a *authmw.Actor) (ticket.StaffScope, error) {
	if a == nil || a.Role != user.RoleStaff {
		return ticket.StaffScope{}, nil
	}

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
// CanViewTicket reports whether an actor may see a ticket.
//
// Exported and context-shaped rather than request-shaped so the MCP server can
// use the same function. Two surfaces deciding ticket visibility independently
// is what produced GHSA-2x4f-j4jv-m2cm; there is one rule and this is it.
func (s *Server) CanViewTicket(ctx context.Context, a *authmw.Actor, t ticket.Ticket) (bool, error) {
	canView, err := s.ticketViewer(ctx, a)
	if err != nil {
		return false, err
	}
	return canView(t), nil
}

// ticketViewer resolves everything CanViewTicket needs about the ACTOR once,
// and returns the per-ticket check.
//
// CanViewTicket recomputes the actor's side on every call — the scope-enforced
// setting, then the caller's groups and each group's scope rules. That is fine
// for one ticket and ruinous for thousands: the admin-wide audit view checks
// a ticket per distinct entry, and with distinct out-of-scope tickets a
// 50,000-row walk measured ~31s, past the server's own 30s WriteTimeout.
// Everything about the actor is constant for a request, so it is resolved
// here once. CanViewTicket is built on this, so there is still exactly one
// rule — two surfaces deciding visibility independently is what produced
// GHSA-2x4f-j4jv-m2cm.
func (s *Server) ticketViewer(ctx context.Context, a *authmw.Actor) (func(ticket.Ticket) bool, error) {
	if a == nil {
		return func(ticket.Ticket) bool { return false }, nil
	}

	if !s.adminSvc.TicketScopeEnforced(ctx) {
		// Legacy behaviour: admins and staff see everything, users see their own.
		if a.Role == user.RoleUser {
			uid := a.UserID
			return func(t ticket.Ticket) bool {
				return t.ReporterUserID != nil && *t.ReporterUserID == uid
			}, nil
		}
		return func(ticket.Ticket) bool { return true }, nil
	}

	scope, err := s.staffScopeFor(ctx, a)
	if err != nil {
		return nil, err
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	return func(t ticket.Ticket) bool { return ticket.CanView(t, actor, scope) }, nil
}

// canViewTicket is the request-shaped wrapper used by the HTTP handlers.
func (s *Server) canViewTicket(r *http.Request, t ticket.Ticket) (bool, error) {
	return s.CanViewTicket(r.Context(), authmw.GetActor(r), t)
}

// TicketVisibility resolves an actor's authority into the mode a listing
// applies. Paired with CanViewTicket: that answers "may this actor see this
// ticket", this answers "which tickets may a query return". They must agree,
// which is why they live together.
func (s *Server) TicketVisibility(ctx context.Context, a *authmw.Actor) (ticket.Visibility, error) {
	if a == nil {
		// No actor, no tickets. Handlers reject this earlier; returning the
		// narrowest mode means a missed check still fails closed.
		return ticket.VisibilityReporter, nil
	}
	if a.Role == user.RoleUser {
		return ticket.VisibilityReporter, nil
	}
	if a.Role == user.RoleAdmin || !s.adminSvc.TicketScopeEnforced(ctx) {
		// Legacy behaviour when enforcement is off: staff see everything.
		return ticket.VisibilityAll, nil
	}
	return ticket.VisibilityScoped, nil
}
