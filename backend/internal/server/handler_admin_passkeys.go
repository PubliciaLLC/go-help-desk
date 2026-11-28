package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// The administrator's half of the passkey surface (#615): see a user's keys and
// remove ONE of them, instead of Reset MFA's all-or-nothing.
//
// Neither route is behind requirePasskeys. Cleaning up a lost key has to work
// while passkeys are switched off (#494), and neither touches the ceremony
// service.

// GET /api/v1/admin/users/{id}/passkeys
//
// The same JSON as GET /me/passkeys. An unknown account is 404, so a typo in
// the id does not read as "this person has no keys".
func (s *Server) handleAdminListPasskeys(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	if _, err := s.users.GetByIDAdmin(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	creds, err := s.passkeyStore.ListByUser(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, creds)
}

// DELETE /api/v1/admin/users/{id}/passkeys/{credId}
//
// Removes one key and ends all of the account's sessions (D1). Refused for the
// administrator's own account (D2): adminRouter checks only that the session
// passed MFA, and a session that owed no factor has passed it without proving
// one, so this route would let such a session remove its own factor around
// requireFactorOrFirstEnrolment. The Account page is the way for your own, and
// it holds that guard.
func (s *Server) handleAdminDeletePasskey(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	credID, err := uuid.Parse(chi.URLParam(r, "credId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid credential id")
		return
	}
	actor := authmw.GetActor(r)
	if actor == nil || actor.UserID == uuid.Nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "sign in first")
		return
	}
	if actor.UserID == userID {
		Error(w, http.StatusConflict, "own_account", ownSecondFactorMessage)
		return
	}
	if err := s.users.AdminRemovePasskey(r.Context(), userID, credID, &actor.UserID); err != nil {
		if errors.Is(err, webauthn.ErrNotFound) {
			// "Not yours" and "does not exist" answer the same way, as for the
			// owner's own delete.
			Error(w, http.StatusNotFound, "not_found", "no such passkey on this account")
			return
		}
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownSecondFactorMessage is what an administrator is told when they aim an
// admin route at their own second factor (D2, D3).
const ownSecondFactorMessage = "Manage your own second factor from the Account page. " +
	"Another administrator can reset yours, or reset-factors can on the server."
