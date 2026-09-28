package server

import (
	"context"
	"fmt"
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
		// The first category, because a ticket cannot be filed without one
		// and setup is the only moment anybody is being asked to configure
		// anything. Optional in the request so an existing caller does not
		// break; the wizard supplies it. See #323.
		Category string `json:"category"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.DisplayName) == "" || body.Password == "" {
		Error(w, http.StatusBadRequest, "bad_request", "email, display_name, and password are required")
		return
	}

	// The first category, created BEFORE the administrator and only when none
	// exists.
	//
	// Order matters here in a way it usually does not. Setup does not reopen:
	// once a user exists, /setup answers 409 forever. So a failure between
	// these two steps must not be able to leave an instance with an
	// administrator and no category — which is precisely the state that
	// cannot file a ticket, and the state this exists to prevent. Doing the
	// category first leaves only two failure modes, both retryable: nothing
	// happened at all, or a category exists and no user does. The "only when
	// none exists" test is what makes the retry reuse it rather than stack up
	// duplicates.
	//
	// Not a transaction spanning both: the two services own separate stores
	// and nothing else in this codebase composes them that way. Ordering buys
	// the same safety here because the category is harmless on its own.
	if err := s.ensureFirstCategory(r.Context(), body.Category); err != nil {
		handleError(w, err)
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

// defaultCategoryName is what the first category is called when setup was not
// given a name. Chosen to be obviously replaceable rather than clever: an
// operator who wants a real taxonomy renames it, and one who does not never
// has to learn the concept exists in order to file their first ticket.
const defaultCategoryName = "General"

// ensureFirstCategory creates the instance's first category if it has none.
//
// Idempotent on purpose — see the ordering note in handleSetup. A blank name
// is not a bad request: somebody finishing setup should not be stopped over a
// field whose only job is to stop them being stopped later.
func (s *Server) ensureFirstCategory(ctx context.Context, name string) error {
	existing, err := s.categories.ListCategories(ctx, false)
	if err != nil {
		return fmt.Errorf("checking for existing categories: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultCategoryName
	}
	if _, err := s.categories.CreateCategory(ctx, name, 0); err != nil {
		return fmt.Errorf("creating the first category: %w", err)
	}
	return nil
}
