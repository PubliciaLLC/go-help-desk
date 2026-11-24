package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Disabling a user is the standard incident response: it is what an operator
// does first when an account is believed compromised. Sessions are revoked on
// disable (handleUpdateUser calls DeleteForUser), but an API key is a separate
// credential that no revocation path touched — APIKeyAuth checked only the
// key's own expiry, never whether the owning user was still active.
//
// GHSA-p2wx-wvfr-7h3p tells operators that administrative disable now cuts
// access. This is the test that makes that true rather than aspirational.
func TestDisabledUser_APIKeyStopsWorking(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Baseline: the staff key works before the disable, so a later 401 is the
	// disable doing its job rather than a broken fixture.
	resp := h.do(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "staff API key should work before disable")

	resp = h.doAsAdmin(t, http.MethodPatch,
		fmt.Sprintf("/api/v1/admin/users/%s", h.staffID),
		map[string]any{"disabled": true})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.do(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"a disabled user's API key must not authenticate")

	// Writes too, not just reads — the read path and the write path authenticate
	// through the same middleware, but asserting only the read would not show it.
	resp = h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "should not be created", "description": "x",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"a disabled user's API key must not authorise writes")
}

// Re-enabling restores access. Without this, a fix that permanently bricked the
// key on any disable would pass the test above.
func TestReEnabledUser_APIKeyWorksAgain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPatch,
		fmt.Sprintf("/api/v1/admin/users/%s", h.staffID),
		map[string]any{"disabled": true})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.do(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = h.doAsAdmin(t, http.MethodPatch,
		fmt.Sprintf("/api/v1/admin/users/%s", h.staffID),
		map[string]any{"disabled": false})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.do(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"re-enabling the user must restore their API key")
}
