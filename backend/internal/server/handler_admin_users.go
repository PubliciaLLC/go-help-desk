package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Admin user management: listing, creation, profile edits, password reset.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Users ────────────────────────────────────────────────────────────────��───

// adminUserSummary is the list-view representation of a user for admins.
// It exposes disabled status and auth type without returning sensitive fields.
type adminUserSummary struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        user.Role `json:"role"`
	Disabled    bool      `json:"disabled"`
	AuthType    string    `json:"auth_type"` // "local", "saml", "both"
	MFAEnabled  bool      `json:"mfa_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// adminUserDetail extends the summary with group memberships.
type adminUserDetail struct {
	adminUserSummary
	HasPassword bool          `json:"has_password"`
	Groups      []group.Group `json:"groups"`
}

func authTypeOf(u user.User) string {
	hasLocal := u.PasswordHash != ""
	hasSAML := u.SAMLSubject != ""
	switch {
	case hasLocal && hasSAML:
		return "both"
	case hasSAML:
		return "saml"
	default:
		return "local"
	}
}

func toAdminSummary(u user.User) adminUserSummary {
	return adminUserSummary{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		Disabled:    u.Disabled,
		AuthType:    authTypeOf(u),
		MFAEnabled:  u.MFAEnabled,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.ListAdmin(r.Context(), 200, 0)
	if err != nil {
		handleError(w, err)
		return
	}
	out := make([]adminUserSummary, len(users))
	for i, u := range users {
		out[i] = toAdminSummary(u)
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	u, err := s.users.Create(r.Context(), user.CreateUserInput{
		Email:       body.Email,
		DisplayName: body.DisplayName,
		Role:        user.Role(body.Role),
		Password:    body.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	u, err := s.users.GetByIDAdmin(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	groups, err := s.groups.ListGroupsForUser(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	detail := adminUserDetail{
		adminUserSummary: toAdminSummary(u),
		HasPassword:      u.PasswordHash != "",
		Groups:           groups,
	}
	JSON(w, http.StatusOK, detail)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	var body struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Role        *string `json:"role"`
		Disabled    *bool   `json:"disabled"`
		ResetMFA    bool    `json:"reset_mfa"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// Disable/enable toggle (processed before any profile update).
	if body.Disabled != nil {
		if *body.Disabled {
			if err := s.users.Disable(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
		} else {
			if err := s.users.Enable(r.Context(), id); err != nil {
				handleError(w, err)
				return
			}
		}
	}

	// MFA reset (works regardless of disabled state).
	//
	// This deliberately does NOT force a password rotation. It briefly did, to
	// defend a chain where someone already holding the password burns the
	// victim's TOTP budget, waits for an administrator to clear MFA, and
	// enrols first. That chain is real but needs a targeted attacker who
	// already has valid credentials and is racing the victim; the ordinary
	// reason for a reset is a new phone. Making every routine reset also
	// require conveying a new password out of band was a large, permanent cost
	// for a rare one, so the requirement was removed. Password reset remains
	// available separately when an administrator judges it warranted.
	if body.ResetMFA {
		if err := s.users.ResetMFA(r.Context(), id); err != nil {
			handleError(w, err)
			return
		}
	}

	// Profile field updates.
	if body.DisplayName != nil || body.Email != nil || body.Role != nil {
		u, err := s.users.GetByIDAdmin(r.Context(), id)
		if err != nil {
			handleError(w, err)
			return
		}
		if body.DisplayName != nil {
			u.DisplayName = *body.DisplayName
		}
		if body.Email != nil {
			u.Email = *body.Email
		}
		if body.Role != nil {
			u.Role = user.Role(*body.Role)
		}
		if err := s.users.Update(r.Context(), u); err != nil {
			handleError(w, err)
			return
		}
	}

	// Re-fetch and return the updated detail view.
	u, err := s.users.GetByIDAdmin(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	groups, err := s.groups.ListGroupsForUser(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	detail := adminUserDetail{
		adminUserSummary: toAdminSummary(u),
		HasPassword:      u.PasswordHash != "",
		Groups:           groups,
	}
	JSON(w, http.StatusOK, detail)
}

func (s *Server) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if err := s.users.AdminSetPassword(r.Context(), id, body.NewPassword); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	if err := s.users.SoftDelete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
