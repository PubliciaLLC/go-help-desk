package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

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

		next.ServeHTTP(w, r)
	})
}

// canViewTicketID fetches a ticket by id and reports whether the caller may see
// it.
//
// For the cases the path gate cannot cover: a request that names a SECOND
// ticket in its body. requireTicketAccess authorises {id} and nothing else, so
// a handler taking another ticket id has to ask separately.
func (s *Server) canViewTicketID(r *http.Request, id uuid.UUID) (bool, error) {
	t, err := s.tickets.GetByID(r.Context(), id)
	if err != nil {
		return false, err
	}
	return s.canViewTicket(r, t)
}
