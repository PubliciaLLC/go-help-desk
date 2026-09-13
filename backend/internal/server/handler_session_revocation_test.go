package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Sessions used to be stateless signed cookies, so nothing could invalidate
// one: a disabled account kept working until its cookie expired, logout only
// cleared the browser's copy, and an admin clearing MFA left a stale
// MFAPassed=true in every existing cookie.
//
// Each test below logs in for real, confirms the session works, performs the
// event, and confirms the SAME cookie stops working. A test that only checked
// the database row would pass against a store that never consulted it.

// loggedIn returns a session holding a live cookie for the seeded staff user.
func loggedIn(t *testing.T, h *harness) *session {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition: the session works")
	return s
}

func TestSessionRevocation(t *testing.T) {
	t.Run("disabling the user kills the session", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
			map[string]any{"disabled": true})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"a disabled account must stop working immediately, not when its cookie expires")
	})

	t.Run("a role change kills the session", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
			map[string]any{"role": string(user.RoleUser)})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"the old role must not stay live in an existing session")
	})

	// Renaming somebody is not a security event and must not sign them out.
	t.Run("an ordinary edit leaves the session alone", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
			map[string]any{"display_name": "Renamed Person"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusOK, res.StatusCode,
			"a display-name change must not log anyone out")
	})

	// The chain that made this urgent: a cookie minted before an MFA reset
	// carries MFAPassed=true, and handleMFAEnrollStart passes that straight in
	// as allowReenroll — so it could enrol its own authenticator afterwards.
	t.Run("an MFA reset kills the session", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		enrollMFA(t, ctx, h.userSvc, h.staffID)
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
			map[string]any{"reset_mfa": true})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"a cookie minted before the reset must not survive it")

		res, _ = s.send(t, http.MethodGet, "/api/v1/me/mfa/enroll", nil)
		require.NotEqual(t, http.StatusOK, res.StatusCode,
			"and it must not be able to enrol a replacement authenticator")
	})

	t.Run("an admin password reset kills the session", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users/"+h.staffID.String()+"/password",
			map[string]any{"new_password": "a-brand-new-password"})
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"a reset is usually a response to compromise; old sessions must go")
	})

	// Logout now means something server-side: a copy of the cookie taken
	// beforehand is equally dead, which a stateless cookie could never do.
	t.Run("logout invalidates the session server-side", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		// A second client holding the same cookie — a stolen copy.
		thief := &session{h: h, jar: s.jar}

		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/logout", nil)
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		res, _ = thief.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"logout must kill the session, not just the browser's copy of the cookie")
	})
}

// Changing your own password evicts everyone else holding it, without signing
// you out of the tab you used to change it.
func TestChangePassword_KeepsTheCallerSignedIn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	caller := loggedIn(t, h)
	other := &session{h: h, jar: caller.jar}
	_ = other

	elsewhere := loggedIn(t, h) // a second, independent login

	res, body := caller.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"password": "a-much-better-password"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, _ = caller.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"the session that changed the password must survive")

	res, _ = elsewhere.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"every other session must be evicted")
}
