package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Self-service API keys (#450): /me/api-keys, for administrators and staff.
//
// A key is a long-lived bearer credential that acts as its owner. Three rules
// keep that from becoming a way around the accounts it belongs to:
//
//   - The routes are refused to API keys and OAuth clients (DenyMachineCredentials
//     in routes.go). A leaked key that could mint keys would turn one leak into
//     as many as the attacker likes, and revoking the first would remove none
//     of the rest.
//   - Staff may create one only while an administrator has allowed it
//     (staff_can_create_api_keys). A requester never can, whatever it says.
//   - A key's scopes can never exceed its owner's role (auth.ScopesForRole).
//
// Listing and revoking are not behind the setting. They only ever remove
// access, and a staff member must be able to see and revoke a key an
// administrator issued for them even when they may not create their own.

// mayCreateAPIKeys reports whether the signed-in role may mint a key.
func (s *Server) mayCreateAPIKeys(r *http.Request, role user.Role) bool {
	switch role {
	case user.RoleAdmin:
		return true
	case user.RoleStaff:
		return s.adminSvc.StaffCanCreateAPIKeys(r.Context())
	}
	return false
}

func (s *Server) handleListMyAPIKeys(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	keys, err := s.authStore.ListByUser(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	years := s.adminSvc.APIKeyMaxLifetimeYears(r.Context())
	now := time.Now()
	type view struct {
		auth.APIKey
		BeyondLimit bool `json:"beyond_limit"`
	}
	out := make([]view, len(keys)) // [] and not null: "no keys" is an answer
	for i, k := range keys {
		out[i] = view{k, beyondLimit(years, k.ExpiresAt, now)}
	}
	JSON(w, http.StatusOK, out)
}

// handleListMyAPIKeyScopes serves the scopes the caller's own role can put on
// a key, so the picker offers exactly what the server will accept.
func (s *Server) handleListMyAPIKeyScopes(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	type scopeInfo struct {
		Scope    string `json:"scope"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
	}
	offered := auth.ScopesForRole(string(a.Role))
	out := make([]scopeInfo, len(offered))
	for i, sc := range offered {
		out[i] = scopeInfo{Scope: sc.String(), Resource: sc.Resource, Action: string(sc.Action)}
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateMyAPIKey(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if !s.mayCreateAPIKeys(r, a.Role) {
		Error(w, http.StatusForbidden, "api_keys_not_allowed",
			"an administrator has not allowed staff to create their own API keys")
		return
	}
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
		// Present only to be refused. Ignoring it would hand the key to the
		// caller when they asked for it to go to someone else, and a request
		// that quietly does something other than what it says is how a key
		// ends up with the wrong person.
		UserID *uuid.UUID `json:"user_id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.UserID != nil {
		Error(w, http.StatusBadRequest, "bad_request",
			"user_id is not accepted here: this route issues a key for you. An administrator can issue one for someone else at POST /api/v1/admin/api-keys")
		return
	}
	if !checkKeyScopes(w, body.Scopes, a.Role) {
		return
	}
	expiresAt, ok := s.checkKeyExpiry(w, r.Context(), body.ExpiresAt)
	if !ok {
		return
	}
	s.issueAPIKey(w, r, ownerOfKey{id: a.UserID, role: a.Role}, body.Name, body.Scopes, expiresAt)
}

func (s *Server) handleDeleteMyAPIKey(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	// The owner is part of the delete itself, so someone else's key is "not
	// found", the same answer as a key that does not exist.
	mine, err := s.authStore.ListWithOwner(r.Context(), &id)
	if err != nil {
		handleError(w, err)
		return
	}
	if err := s.authStore.DeleteForUser(r.Context(), id, a.UserID); err != nil {
		handleError(w, err)
		return
	}
	// Read for the audit entry's scopes only. Key ids are random and owners
	// never change, so a key DeleteForUser just removed was in that read.
	var scopes []string
	var expires *time.Time
	if len(mine) > 0 {
		scopes, expires = mine[0].Scopes, mine[0].ExpiresAt
	}
	s.auditAPIKey(r.Context(), a.UserID, "api_key_revoked", id, a.UserID, scopes, expires)
	w.WriteHeader(http.StatusNoContent)
}
