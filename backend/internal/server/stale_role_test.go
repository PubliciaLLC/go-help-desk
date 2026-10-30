package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A demoted administrator cannot carry their old role forward.
//
// The session payload records the role it was minted with, and that is what
// the middleware trusted. Demoting somebody revokes their sessions — but a
// request already in flight finishes afterwards, and a handler that re-mints
// a session does so from the role it read on the way in. A password change is
// the worst of those: the account holder picks the moment, and it spends
// fifty-odd milliseconds hashing. Measured on a real server, the demotion
// landed 52ms into a 93ms request, and the newly issued cookie then read
// /admin/users successfully. The demotion was in the database and the
// attacker was an administrator again.
//
// Disabling and deleting were never vulnerable, because the session query has
// always joined users to drop those rows. Role was the one piece of authority
// still being carried rather than looked up.
func TestSession_TakesTheRoleFromTheDatabase(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	victim, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "carries-a-role@test.local", DisplayName: "Carries A Role",
		Role: user.RoleAdmin, Password: "password",
	})
	require.NoError(t, err)

	theirs := &session{h: h}
	res, body := theirs.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "carries-a-role@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	// The session works, and it is an administrator's.
	res, _ = theirs.send(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition: an admin session")

	// Demote them in the database WITHOUT revoking, which is what an
	// in-flight request effectively reproduces: the revocation has happened
	// and a cookie minted from the old role is in play.
	require.NoError(t, h.userSvc.SetRole(ctx, victim.ID, user.RoleUser))

	// The same cookie must no longer be an administrator's.
	res, body = theirs.send(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"the cookie is still carrying the old role, so a demotion can be outrun: %s", body)

	// And it is still a valid session for what the account may now do.
	res, _ = theirs.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"demoting somebody should lower what they can do, not log them out of everything")
}

// The reverse, so the fix cannot be "always deny": a promotion takes effect
// on the existing session too.
func TestSession_APromotionAlsoTakesEffect(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	staff := &session{h: h}
	res, body := staff.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, _ = staff.send(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "precondition: not an admin")

	require.NoError(t, h.userSvc.SetRole(ctx, h.staffID, user.RoleAdmin))

	res, body = staff.send(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"the role is read from the database, so a promotion should apply too: %s", body)
}
