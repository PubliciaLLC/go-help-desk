package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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

// The lockout check. A passkey here is only ever a second factor after the
// password; there is no passwordless sign-in, so the password remains a way
// in. What must not happen is an account the server keeps asking for a key it
// cannot check. It is asked to enrol TOTP instead, and enrolment must accept
// it even though the account "already has a passkey".
func TestPasskeysUnavailable_PasskeyOnlyAccountFallsBackToTOTP(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	ctx := context.Background()

	setup := loggedInAdmin(t, h)
	// Not an enforced role: the account volunteered the passkey, and losing
	// that protection silently would be the other way to get this wrong.
	res, body := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "stranded")))

	s := &session{h: h}
	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, false, got["passkey_needed"], "asked for a key the server cannot check")
	require.Equal(t, true, got["mfa_enrollment_needed"], "the account was not offered a way forward")

	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"enrolment refused an account whose passkey cannot be used, so it can never sign in: %s", body)
}
