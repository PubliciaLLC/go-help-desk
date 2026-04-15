package server

import (
	"net/http"
	"strings"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// GET /api/v1/setup/status
// Returns {"needed": true} when no users exist, {"needed": false} otherwise.
// Always accessible without authentication.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.users.HasUsers(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]bool{"needed": !hasUsers})
}

// POST /api/v1/setup
// Creates the first admin account. Returns 409 Conflict once any user exists.
// Always accessible without authentication.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.users.HasUsers(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if hasUsers {
		Error(w, http.StatusConflict, "already_configured", "setup has already been completed")
		return
	}

	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.DisplayName) == "" || body.Password == "" {
		Error(w, http.StatusBadRequest, "bad_request", "email, display_name, and password are required")
		return
	}

	u, err := s.users.Create(r.Context(), user.CreateUserInput{
		Email:       body.Email,
		DisplayName: body.DisplayName,
		Role:        user.RoleAdmin,
		Password:    body.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}

	// Never expose the password hash.
	u.PasswordHash = ""

	JSON(w, http.StatusCreated, u)
}
