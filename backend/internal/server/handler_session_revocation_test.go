package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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

		res, _ = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.NotEqual(t, http.StatusOK, res.StatusCode,
			"and it must not be able to enrol a replacement authenticator")
	})

	// SoftDelete is an UPDATE, so the sessions table's ON DELETE CASCADE never
	// fires. Shipped without a test on the first pass, against this project's
	// own "no implementation without a test" rule.
	t.Run("deleting the user kills the session", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := loggedIn(t, h)

		res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/users/"+h.staffID.String(), nil)
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"a deleted user's session must not outlive them")
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
	// A copy of the caller's own cookie — the one most likely to have been
	// stolen, since it is the session they are sitting in. An earlier version
	// of this test built this and then discarded it with `_ = other`, which
	// looked like coverage and asserted nothing; the assertion below fails
	// against an implementation that re-uses the caller's session id.
	thief := &session{h: h, jar: caller.jar}

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

	res, _ = thief.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode,
		"a stolen copy of the caller's own cookie must die too — evicting "+
			"everyone except the person who copied your live session is no eviction")
}

// Session fixation. Moving session state server-side removed an accidental
// protection: with a stateless cookie the cookie WAS the state, so logging in
// overwrote it with a payload the attacker never saw. With a row keyed by an
// id the attacker can plant, reusing that id across authentication hands them
// the authenticated session.
//
// Verified as a working attack before the fix — the planted cookie returned
// the victim's identity from /me.
func TestSessionFixation_IdRotatesOnPrivilegeChange(t *testing.T) {
	assertRotates := func(t *testing.T, h *harness, authenticate func(s *session)) {
		t.Helper()
		attacker := &session{h: h}

		// Any valid id will do; the attacker gets one by logging in as
		// themselves. The signature proves the server issued it, not who owns it.
		res, _ := attacker.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "user@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "attacker obtains an id")
		planted := attacker.jar
		require.NotEmpty(t, planted)

		// The victim's browser is made to carry it, then the victim authenticates.
		victim := &session{h: h, jar: planted}
		authenticate(victim)

		// The attacker still holds the ORIGINAL cookie.
		stale := &session{h: h, jar: planted}
		res, body := stale.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"the planted id must not survive authentication; body %s", body)
	}

	t.Run("local login", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		assertRotates(t, h, func(s *session) {
			res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
				map[string]any{"email": "staff@test.local", "password": "password"})
			require.Equal(t, http.StatusOK, res.StatusCode)
		})
	})

	// The id must rotate at EVERY privilege change, so each step needs its own
	// planted id. An earlier version of this subtest planted a pre-login id and
	// then logged in — but login rotates, so the assertion passed without ever
	// exercising verification. Neutering rotation in handleMFAVerify left it
	// green. The cookie captured here is the one login just issued, which is
	// the only id that can prove the verify step rotates.
	t.Run("MFA verification rotates the post-login id", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
		require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
		enrollMFA(t, ctx, h.userSvc, h.staffID)

		u, err := h.userSvc.GetByID(ctx, h.staffID)
		require.NoError(t, err)

		victim := &session{h: h}
		res, _ := victim.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode)

		// The password-only session id, captured before the second factor.
		halfAuthed := &session{h: h, jar: victim.jar}

		code, err := totp.GenerateCode(u.MFASecret, time.Now())
		require.NoError(t, err)
		res, body := victim.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
			map[string]any{"code": code})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

		res, _ = halfAuthed.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode,
			"the id held before the second factor must not survive passing it")

		// And the victim's own session must still work.
		res, _ = victim.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}
