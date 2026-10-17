package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Keys can't set passwords (CTO, 2026-10-16). An API key or OAuth client may
// still create, edit and disable users, but it may not set a password or clear
// a second factor for anybody. Otherwise a key holding users:write turns into a
// person: make an account with a password, sign in as it, and from that session
// mint a key that survives the revocation of the first (POST /me/api-keys).

const usersPath = "/api/v1/admin/users"

func (h *harness) loginStatus(t *testing.T, email, password string) int {
	t.Helper()
	s := &session{h: h}
	res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": email, "password": password})
	return res.StatusCode
}

// The reviewer's bypass, step by step. It must fail at step 1, so there is no
// account to sign in as and nothing to mint a key from.
func TestMachineCredential_CannotBuildASessionOutOfAUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Step 1: the key creates a staff account with a password it knows.
	res := h.doAsAdmin(t, http.MethodPost, usersPath, map[string]any{
		"email": "planted@test.local", "display_name": "Planted", "role": "staff",
		"password": "a-password-the-key-chose",
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "step 1 must fail: %s", b)
	require.Equal(t, "session_required", errCode(t, b))

	// Step 2: so there is nothing to sign in as.
	require.Equal(t, http.StatusUnauthorized, h.loginStatus(t, "planted@test.local", "a-password-the-key-chose"))
	_, err := h.userSvc.GetByEmail(context.Background(), "planted@test.local")
	require.Error(t, err, "the account was not created")
}

func TestMachineCredential_CannotTakeOverAnAccountThroughItsPassword(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, usersPath+"/"+h.staffID.String()+"/password",
		map[string]any{"new_password": "a-password-the-key-chose"})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
	require.Equal(t, "session_required", errCode(t, b))

	require.Equal(t, http.StatusUnauthorized, h.loginStatus(t, "staff@test.local", "a-password-the-key-chose"))
	require.Equal(t, http.StatusOK, h.loginStatus(t, "staff@test.local", "password"), "the real password still works")
}

func TestMachineCredential_CannotTakeOverAnAccountThroughItsSecondFactor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, usersPath+"/"+h.staffID.String(), map[string]any{"reset_mfa": true})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
	require.Equal(t, "session_required", errCode(t, b))

	entries, err := h.auditStore.ListByEntity(context.Background(), "user", h.staffID, 20, 0)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotEqual(t, "mfa_reset", e.Action, "nothing was reset")
	}

	// And a refused request changed nothing else it carried.
	res = h.doAsAdmin(t, http.MethodPatch, usersPath+"/"+h.staffID.String(),
		map[string]any{"reset_mfa": true, "display_name": "Renamed in the same request"})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, "Staff", u.DisplayName)
}

// Any non-empty value is a password, whitespace included: it would otherwise be
// a way to learn what the check treats as "no password".
func TestMachineCredential_AnyNonEmptyPasswordOnCreateIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, pw := range []string{"x", " ", "a-valid-long-password"} {
		res := h.doAsAdmin(t, http.MethodPost, usersPath, map[string]any{
			"email": "p@test.local", "display_name": "P", "role": "staff", "password": pw,
		})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%q: %s", pw, b)
		require.Equal(t, "session_required", errCode(t, b))
	}
}

// A signed-in administrator is a person, and still can do all of it.
func TestSession_AdministratorCanStillSetPasswordsAndResetFactors(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	res := h.doSession(t, admin, http.MethodPost, usersPath, map[string]any{
		"email": "made@test.local", "display_name": "Made", "role": "staff", "password": "a-valid-long-password",
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	require.Equal(t, http.StatusOK, h.loginStatus(t, "made@test.local", "a-valid-long-password"))

	code, b := h.resetPassword(t, admin, h.staffID, map[string]any{"new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusNoContent, code, "body: %s", b)

	res = h.doSession(t, admin, http.MethodPatch, usersPath+"/"+h.staffID.String(), map[string]any{"reset_mfa": true})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// Keys never change emails (CTO, 2026-10-16). Once a reset link goes to an
// account's address (#451), re-addressing an account sends the link to whoever
// holds the key. Refused whenever the field is present, even unchanged, and
// before anything else in the request is applied.
func TestMachineCredential_CannotChangeAnEmailAddress(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	passwordless, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "nopass@test.local", DisplayName: "No Password", Role: user.RoleStaff,
	})
	require.NoError(t, err)
	require.Empty(t, passwordless.PasswordHash, "precondition: no password")

	for name, c := range map[string]struct {
		id    uuid.UUID
		email string
		name  string
	}{
		"a staff member with a password": {h.staffID, "attacker@evil.test", "Staff"},
		"an account with no password":    {passwordless.ID, "attacker2@evil.test", "No Password"},
		"the same address, unchanged":    {h.staffID, "staff@test.local", "Staff"},
	} {
		t.Run(name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPatch, usersPath+"/"+c.id.String(), map[string]any{
				"email": c.email, "display_name": "Changed in the same request", "disabled": true,
			})
			b := readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
			require.Equal(t, "session_required", errCode(t, b))

			// Nothing else in the request was applied either.
			u, err := h.userSvc.GetByIDAdmin(ctx, c.id)
			require.NoError(t, err)
			require.Equal(t, c.name, u.DisplayName)
			require.False(t, u.Disabled)
			if c.email != "staff@test.local" {
				require.NotEqual(t, c.email, u.Email)
			}
		})
	}

	t.Run("an OAuth client is refused by its role, and changes nothing", func(t *testing.T) {
		client := h.newServiceClient(t, "syncer", allScopes()...)
		res := h.doBearer(t, client.Token, http.MethodPatch, usersPath+"/"+h.staffID.String(),
			map[string]any{"email": "attacker3@evil.test"})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		u, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
		require.NoError(t, err)
		require.Equal(t, "staff@test.local", u.Email)
	})

	t.Run("a signed-in administrator still can", func(t *testing.T) {
		res := h.doAsAdminSession(t, http.MethodPatch, usersPath+"/"+h.staffID.String(),
			map[string]any{"email": "Renamed@Test.Local"})
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		u, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
		require.NoError(t, err)
		require.Equal(t, "renamed@test.local", u.Email)
	})
}
