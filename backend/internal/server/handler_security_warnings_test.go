package server_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The harness configures SESSION_SECRET as "test-session-secret-32-bytes-long!",
// which contains none of the placeholder markers, so a clean instance reports
// nothing. That makes the endpoint's shape testable without pretending the
// harness is misconfigured.

func TestGetSecurityWarnings_RequiresAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Anonymous.
	resp := h.rawGet(t, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// A reporting user must not learn how this instance is configured.
	resp = h.doAsUser(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Staff are not administrators either.
	resp = h.do(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestGetSecurityWarnings_CleanInstanceReportsNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		InsecureSecrets []string `json:"insecure_secrets"`
	}
	decodeJSON(t, resp, &body)
	require.Empty(t, body.InsecureSecrets,
		"the harness secret is not a placeholder, so nothing should be flagged")
}

// TestGetSecurityWarnings_NeverReturnsSecretValues is the assertion that
// matters. The payload exists to say WHICH variable is wrong; leaking the value
// would hand an admin-session reader the signing key itself.
func TestGetSecurityWarnings_NeverReturnsSecretValues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(raw)
	require.NotContains(t, body, "test-session-secret",
		"the response must never contain a secret's value")
	require.NotContains(t, body, "test-jwt-secret")
}
