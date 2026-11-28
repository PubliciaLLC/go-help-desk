package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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

// PUT /api/v1/tickets/{id}/followers/{userId} (#695)
//
// The caller adds somebody else as a follower. Staff and administrators, in a
// session (DenyMachineCredentials, D20). The person must be an active,
// non-service staff or admin account (422 follower_not_eligible, for an
// unknown id too, so the answer does not say which) who can see the ticket
// under ticket_scope_enforced (422 follower_cannot_view_ticket). The domain
// cannot know scope, so the second check is here, as StillEntitled's is.
// Idempotent, 204. The person added is not mailed.
//
// The eligibility, the person's role and the visibility check run before the
// AddFollower transaction, not inside it. If the ticket's category or the
// person's groups change in between, somebody out of scope can end up on the
// list. That is harmless: a follow row grants no access, and StillEntitled
// checks visibility again whenever follower mail is sent.
func (s *Server) handleAddFollower(w http.ResponseWriter, r *http.Request) {
	target, ok := followerUserID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	can, err := s.tickets.CanFollow(ctx, target)
	if err != nil {
		handleError(w, err)
		return
	}
	if !can {
		handleError(w, ticket.ErrFollowerNotEligible)
		return
	}
	// Eligible, so the account is there and is staff or admin: its role is
	// what the visibility rule needs.
	u, err := s.users.GetByID(ctx, target)
	if err != nil {
		handleError(w, err)
		return
	}
	t, err := s.tickets.GetByID(ctx, ticketIDFrom(r))
	if err != nil {
		handleError(w, err)
		return
	}
	sees, err := s.CanViewTicket(ctx, &authmw.Actor{UserID: u.ID, Role: u.Role}, t)
	if err != nil {
		handleError(w, err)
		return
	}
	if !sees {
		Error(w, http.StatusUnprocessableEntity, "follower_cannot_view_ticket",
			"that person cannot see this ticket, so they cannot follow it")
		return
	}
	a := authmw.GetActor(r)
	if err := s.tickets.AddFollower(ctx, t.ID, target, ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/tickets/{id}/followers/{userId} (#695)
//
// The caller removes a follower, whoever added them. Idempotent, 204, for
// somebody who does not follow and for an id that is nobody. Session only.
func (s *Server) handleRemoveFollower(w http.ResponseWriter, r *http.Request) {
	target, ok := followerUserID(w, r)
	if !ok {
		return
	}
	a := authmw.GetActor(r)
	if err := s.tickets.RemoveFollower(r.Context(), ticketIDFrom(r), target, ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func followerUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "userId must be a user id")
		return uuid.Nil, false
	}
	return id, true
}
