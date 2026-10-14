package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #494: when BASE_URL cannot be a relying-party id, main.go starts the server
// with no passkey service. Everything here runs against that server.

const passkeysOffReason = "BASE_URL host must be a domain and not an IP address"

type passkeyStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func passkeyWarning(t *testing.T, h *harness) passkeyStatus {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	var body struct {
		Passkeys *passkeyStatus `json:"passkeys"`
	}
	decodeJSON(t, res, &body)
	require.NotNil(t, body.Passkeys, "security-warnings has no passkeys entry")
	return *body.Passkeys
}

func TestPasskeysUnavailable_SecurityWarningsSaysWhy(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		got := passkeyWarning(t, h)
		require.False(t, got.Available)
		require.Equal(t, passkeysOffReason, got.Reason)
	})
	t.Run("a valid domain is untouched", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		got := passkeyWarning(t, h)
		require.True(t, got.Available)
		require.Empty(t, got.Reason)
	})
}

func TestPasskeysUnavailable_ProvidersSaysSo(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		resp := h.doUnauth(t, http.MethodGet, "/api/v1/auth/providers", nil)
		var out struct {
			Passkeys *bool `json:"passkeys"`
		}
		decodeJSON(t, resp, &out)
		require.NotNil(t, out.Passkeys)
		require.False(t, *out.Passkeys)
	})
	t.Run("on", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		resp := h.doUnauth(t, http.MethodGet, "/api/v1/auth/providers", nil)
		var out struct {
			Passkeys *bool `json:"passkeys"`
		}
		decodeJSON(t, resp, &out)
		require.NotNil(t, out.Passkeys)
		require.True(t, *out.Passkeys)
	})
}

// Every route that would touch the passkey service answers 503, never a nil
// dereference (which the recoverer would turn into a 500 at best).
func TestPasskeysUnavailable_RoutesAnswer503(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()

	s := loggedInAdmin(t, h)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/passkeys"},
		{http.MethodPost, "/api/v1/me/passkeys/register/start"},
		{http.MethodPost, "/api/v1/me/passkeys/register/finish"},
		{http.MethodPost, "/api/v1/auth/local/passkey/start"},
		{http.MethodPost, "/api/v1/auth/local/passkey/finish"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			res, body := s.send(t, rt.method, rt.path, nil)
			require.Equal(t, http.StatusServiceUnavailable, res.StatusCode, string(body))
			var e struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(body, &e))
			require.Equal(t, "passkeys_unavailable", e.Error.Code)
		})
	}

	// And with no session at all: the 503 must not become a way to learn
	// anything, but it also must not panic.
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/passkey/start", nil)
	defer res.Body.Close()
	require.Contains(t, []int{http.StatusUnauthorized, http.StatusServiceUnavailable}, res.StatusCode)
}

// While passkeys are off, an account whose ONLY second factor is a passkey
// cannot be checked, so it must not get in on its password alone: that would
// let a stolen password take over exactly the accounts that chose a stronger
// factor. Every such account is refused, the last administrator included.
// Recovery is on the server side: an administrator's Reset MFA, or
// reset-factors / unlock on the server.
//
// Do not "fix" this by re-adding an exception for the last administrator (one
// existed briefly and was removed on purpose). A password alone must never be
// enough to enrol a second factor, and that is what an exception would allow.

func enforceMFA(t *testing.T, h *harness) {
	t.Helper()
	setup := loggedInAdmin(t, h)
	res, body := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
}

func signIn(t *testing.T, h *harness, email string) (*session, *http.Response, map[string]any) {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": email, "password": "password"})
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return s, res, out
}

func requireRefusedPasskeyUnavailable(t *testing.T, s *session, res *http.Response, out map[string]any) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%v", out)
	errBody, _ := out["error"].(map[string]any)
	require.Equal(t, "passkey_unavailable", errBody["code"])
	require.Contains(t, errBody["message"], "reset your sign-in factors")
	// No session was created.
	me, _ := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, me.StatusCode)
	// And enrolling TOTP with nothing but the password is not possible.
	enrol, _ := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusUnauthorized, enrol.StatusCode)
}

func TestPasskeysUnavailable_PasskeyOnlyNonAdminIsRefused(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(context.Background(), passkeyFor(h.userID, "stranded")))

	s, res, out := signIn(t, h, "user@test.local")
	requireRefusedPasskeyUnavailable(t, s, res, out)
}

// A correct password that is refused for this reason is not a failed guess.
func TestPasskeysUnavailable_RefusalIsNotACountedFailure(t *testing.T) {
	h, cleanup := newHarnessPasskeys(t, 1, "", nil, false)
	defer cleanup()
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(context.Background(), passkeyFor(h.userID, "stranded")))

	for i := 0; i < 3; i++ {
		_, res, _ := signIn(t, h, "user@test.local")
		require.Equal(t, http.StatusForbidden, res.StatusCode, "attempt %d was counted as a failure", i+1)
	}
}

// The last administrator is refused too. Recovery is an administrator's MFA
// reset or reset-factors on the server, not a password-only way in.
func TestPasskeysUnavailable_PasskeyOnlyLastAdminIsRefusedToo(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	requireSoleAdministrator(t, h)
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(context.Background(), passkeyFor(h.adminID, "admin-key")))

	s, res, out := signIn(t, h, "admin@test.local")
	requireRefusedPasskeyUnavailable(t, s, res, out)
}

func TestPasskeysUnavailable_PasskeyOnlyAdminWhoIsNotLastIsRefused(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	ctx := context.Background()
	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "admin2@test.local", DisplayName: "Admin Two", Role: user.RoleAdmin, Password: "password",
	})
	require.NoError(t, err)
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.adminID, "admin-key")))

	s, res, out := signIn(t, h, "admin@test.local")
	requireRefusedPasskeyUnavailable(t, s, res, out)
}

func TestPasskeysUnavailable_AfterAnAdminResetTheUserCanProceed(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	ctx := context.Background()
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "stranded")))
	_, res, _ := signIn(t, h, "user@test.local")
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	admin := loggedInAdmin(t, h)
	r, body := admin.send(t, http.MethodPatch, "/api/v1/admin/users/"+h.userID.String(),
		map[string]any{"reset_mfa": true})
	require.Equal(t, http.StatusOK, r.StatusCode, "%s", body)

	s, res, out := signIn(t, h, "user@test.local")
	require.Equal(t, http.StatusOK, res.StatusCode, "%v", out)
	require.Equal(t, false, out["passkey_needed"])
	enrol, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, enrol.StatusCode, "%s", body)
}

func TestPasskeysUnavailable_PasskeyPlusTOTPUsesTOTP(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	ctx := context.Background()
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "key")))
	require.NoError(t, h.userStore.SetMFA(ctx, h.userID, "ATOTPSECRET", true))

	_, res, out := signIn(t, h, "user@test.local")
	require.Equal(t, http.StatusOK, res.StatusCode, "%v", out)
	require.Equal(t, true, out["mfa_needed"])
	require.Equal(t, false, out["passkey_needed"])
}
