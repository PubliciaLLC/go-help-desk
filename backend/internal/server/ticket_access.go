package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// ticketCtxKey carries the resolved, access-checked ticket into handlers.
type ticketCtxKey struct{}

// ticketFromContext returns the ticket requireTicketAccess already resolved and
// authorised, and whether it was present.
func ticketFromContext(r *http.Request) (ticket.Ticket, bool) {
	t, ok := r.Context().Value(ticketCtxKey{}).(ticket.Ticket)
	return t, ok
}

// requireTicketAccess resolves the {id} path parameter and refuses the request
// unless the caller may see that ticket.
//
// It is middleware on the whole /tickets/{id} subtree rather than a call inside
// each handler, because the per-handler version is what failed: the rule was
// applied to GET /{id} and PATCH /{id} and silently omitted from the fifteen
// routes beneath them. GET /{id}/replies then returned the entire thread —
// staff-only internal notes included — to any signed-in user holding a ticket
// UUID, while GET /{id} on the same ticket correctly answered 403.
//
// As middleware the check cannot be forgotten by a new route, which is the
// property that matters more than the check itself.
func (s *Server) requireTicketAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "id")

		// Both forms, matching what GET /{id} has always accepted. Resolving
		// here rather than per handler also means the tracking-number path is
		// access-checked, which it was not everywhere before.
		var (
			t   ticket.Ticket
			err error
		)
		if uid, parseErr := uuid.Parse(raw); parseErr == nil {
			t, err = s.tickets.GetByID(r.Context(), uid)
		} else {
			t, err = s.tickets.GetByTrackingNumber(r.Context(), ticket.TrackingNumber(strings.ToUpper(raw)))
		}
		if err != nil {
			handleError(w, err)
			return
		}

		ok, err := s.canViewTicket(r, t)
		if err != nil {
			handleError(w, err)
			return
		}
		if !ok {
			Error(w, http.StatusForbidden, "forbidden", "not your ticket")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ticketCtxKey{}, t)))
	})
}

// visibleReplies drops internal notes for a caller who is not staff.
//
// Internal notes are staff-to-staff. Access to the ticket is not access to
// them: the reporter is allowed to read their own thread and must still not see
// them. handleListReplies returned the raw rows, so every internal note on a
// reporter's own ticket was readable by that reporter.
//
// The same rule exists in internal/mcp for the MCP surface; both consume the
// ticket's replies and neither should be the only place it is enforced.
func visibleReplies(replies []ticket.Reply, a *authmw.Actor) []ticket.Reply {
	if a != nil && (a.Role == user.RoleAdmin || a.Role == user.RoleStaff) {
		return replies
	}
	out := make([]ticket.Reply, 0, len(replies))
	for _, rep := range replies {
		if !rep.Internal {
			out = append(out, rep)
		}
	}
	return out
}
