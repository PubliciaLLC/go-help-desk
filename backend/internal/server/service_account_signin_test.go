package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A service user is the identity an OAuth client acts as, and it never signs
// in (#420, decisions on #434). One test per way in.

func TestServiceAccount_CannotSignInWithAPassword(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	// The synthetic address is the only identifier the account has; the
	// password is whatever a guesser tries, including the empty one.
	email := "oauth-" + c.ID.String() + "@service-account.invalid"
	for _, pw := range []string{"", "password", "oauth"} {
		resp := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]string{"email": email, "password": pw})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "password %q", pw)
	}
}

func TestServiceAccount_AdminCannotGiveItAPasswordOrMFAOrAdminRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	// One session up front: a refused write below can abort the harness
	// transaction, after which no new session could be created.
	admin := h.session(t, asAdmin)
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	base := "/api/v1/admin/users/" + c.UserID.String()

	// The allowed edits first: the refusals below reach database constraints,
	// which abort the harness transaction and with it the session lookup.
	t.Run("staff and reporting roles are allowed", func(t *testing.T) {
		for _, role := range []string{"user", "staff"} {
			resp := h.doSession(t, admin, http.MethodPatch, base, map[string]any{"role": role})
			require.Equal(t, http.StatusOK, resp.StatusCode, role)
		}
	})
	t.Run("rename is allowed", func(t *testing.T) {
		resp := h.doSession(t, admin, http.MethodPatch, base, map[string]any{"display_name": "Nagios (prod)"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("password", func(t *testing.T) {
		resp := h.doSession(t, admin, http.MethodPost, base+"/password", map[string]string{"new_password": "a-long-enough-passphrase"})
		require.Equal(t, http.StatusConflict, resp.StatusCode)
		require.Equal(t, "service_account", errorCode(t, resp))
	})
	t.Run("reset MFA", func(t *testing.T) {
		resp := h.doSession(t, admin, http.MethodPatch, base, map[string]any{"reset_mfa": true})
		require.Equal(t, http.StatusConflict, resp.StatusCode)
		require.Equal(t, "service_account", errorCode(t, resp))
	})
	t.Run("admin role", func(t *testing.T) {
		resp := h.doSession(t, admin, http.MethodPatch, base, map[string]any{"role": "admin"})
		// A signed-in administrator reaches the account's own constraint: a
		// service account is never an administrator (users_service_account_inert).
		require.Equal(t, http.StatusConflict, resp.StatusCode)
		require.Equal(t, "service_account", errorCode(t, resp))
	})
}

// The role refusal for a session-driven administrator, which is the path the
// admin page uses; the harness's API key is refused earlier for being a machine.
func TestServiceAccount_SessionAdminCannotPromoteItEither(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	err := h.userSvc.SetRole(t.Context(), c.UserID, user.RoleAdmin)
	require.ErrorIs(t, err, user.ErrServiceAccount)
}

func TestServiceAccount_NoSessionForItLoads(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	// A session row written for it directly: the state a forged or leftover
	// one would be in. The request must not be treated as signed in.
	// The control comes first: the same shape of session for an ordinary
	// staff user works, so a 401 below is the service user and not the fixture.
	control := h.sessionCookieFor(t, auth.SessionData{UserID: h.staffID, Role: user.RoleStaff, MFAPassed: true})
	require.Equal(t, http.StatusOK, h.doWithCookie(t, control, "/api/v1/me").StatusCode)

	cookie := h.sessionCookieFor(t, auth.SessionData{UserID: c.UserID, Role: user.RoleStaff, MFAPassed: true})
	require.Equal(t, http.StatusUnauthorized, h.doWithCookie(t, cookie, "/api/v1/me").StatusCode)
}
