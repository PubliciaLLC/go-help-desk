package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// requireTicketAccess resolves the {id} path parameter and refuses the request
// unless the caller may see that ticket.
//
// It is middleware on the whole /tickets/{id} subtree rather than a call inside
// each handler, because the per-handler version is what failed: at 1.1.1 the
// rule was written inline in GET /{id} and GET /{id}/history and nowhere else,
// so PATCH and the fifteen routes beneath them were open. GET /{id}/replies
// returned the entire thread — staff-only internal notes included — to any
// signed-in user holding a ticket UUID, while GET /{id} on the same ticket
// correctly answered 403.
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
			t          ticket.Ticket
			err        error
			identifier string
		)
		if uid, parseErr := uuid.Parse(raw); parseErr == nil {
			identifier = uid.String()
			t, err = s.tickets.GetByID(r.Context(), uid)
		} else {
			identifier = strings.ToUpper(raw)
			t, err = s.tickets.GetByTrackingNumber(r.Context(), ticket.TrackingNumber(identifier))
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
			ticketNotFound(w, identifier)
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

// ticketNotFound answers exactly as GetByID's own not-found error does — same
// status, same code, and (via ticketstore.TicketNotFoundError) the same
// message for the same identifier — for a ticket that exists but the caller
// may not see.
//
// id is whatever the caller used to address the ticket (a UUID's canonical
// string form, or the uppercased tracking number) — the same value a real
// GetByID/GetByTrackingNumber miss would have embedded. Echoing it back
// reveals nothing the caller did not already supply in the request; an early
// version of this helper used a bare "not found" with no identifier, which
// LOOKED identical to a missing-ticket response but was not — wrapNotFound
// embeds "ticket <id>", so a reporter could still tell the two apart by the
// message even once the status code and code string matched.
//
// Used to be 403: a ticket that exists and the caller cannot see answered
// "forbidden", and only a ticket that genuinely does not exist answered
// "not found". Tracking numbers are sequential (GHD-2026-000001,
// ...000002), so any signed-in reporting user could walk them upward and
// learn which numbers exist and roughly how many tickets the instance has —
// an oracle that revealed no content but did reveal existence. MCP already
// answered "not found" for the same case. See #174.
//
// This is a breaking change to the REST API's published contract,
// deliberately made in a beta's bug-fix branch rather than deferred to a
// major version — the probe this closes was judged worse than the break.
func ticketNotFound(w http.ResponseWriter, id string) {
	handleError(w, ticketstore.TicketNotFoundError(id))
}
