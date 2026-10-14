package server

import (
	"net/http"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Ticket followers (#441, part D of #464). All three routes sit behind
// requireTicketAccess, so a ticket the caller cannot see is a 404 and a
// tracking number works as well as a UUID.

// PUT /api/v1/tickets/{id}/follow
//
// The caller follows the ticket. Idempotent: 204 whether or not they already
// did. Registered behind DenyMachineCredentials, because following is a
// person's own preference and an API key or OAuth client is a script acting
// for them, not them (#490); ticket.Service.Follow refuses a service account
// as well, so a caller that is not this route cannot get round it.
func (s *Server) handleFollowTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if err := s.tickets.Follow(r.Context(), ticketIDFrom(r), ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/tickets/{id}/follow
//
// The caller stops following. Idempotent, 204.
func (s *Server) handleUnfollowTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if err := s.tickets.Unfollow(r.Context(), ticketIDFrom(r), ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/tickets/{id}/followers
//
// Who follows the ticket, for staff and administrators. Staff always see it:
// whether to show it was decided once, and it is not behind a setting. A
// requester has no route here; a machine credential with tickets:read may
// read it, since reading discloses nothing the page does not.
func (s *Server) handleListFollowers(w http.ResponseWriter, r *http.Request) {
	followers, err := s.tickets.ListFollowers(r.Context(), ticketIDFrom(r))
	if err != nil {
		handleError(w, err)
		return
	}
	if followers == nil {
		followers = []ticket.Follower{}
	}
	JSON(w, http.StatusOK, followers)
}
