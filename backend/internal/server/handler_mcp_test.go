package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProtectMCP is the transport half of the MCP authorization fix.
//
// The MCP handler is mounted on the root ServeMux in cmd/server, beside /api/
// rather than inside the router that carries the auth middleware. Mounted bare
// — which it was — every tool was reachable with no credentials: an
// unauthenticated caller could read any ticket by tracking number and post
// replies while naming any user as the author.
//
// ProtectMCP applies the same chain as /api/ and narrows the surface to staff
// and admins. The sentinel handler below stands in for the MCP server: what
// matters is whether the request reaches it at all.
func TestProtectMCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
		wantReach  bool
	}{
		{
			name:       "no credentials are refused",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "a garbage key is refused",
			authHeader: "ApiKey not-a-real-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			// The HTTP API lets a RoleUser read their own tickets by scoping
			// each handler. MCP does no such scoping, so a RoleUser must not
			// reach it at all rather than reach it unscoped.
			name:       "a reporting user is refused",
			authHeader: "ApiKey " + h.userKey,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "staff are allowed through",
			authHeader: "ApiKey " + h.apiKey,
			wantStatus: http.StatusOK,
			wantReach:  true,
		},
		{
			name:       "admins are allowed through",
			authHeader: "ApiKey " + h.adminKey,
			wantStatus: http.StatusOK,
			wantReach:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			sentinel := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			h.srv.ProtectMCP(sentinel).ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			require.Equal(t, tc.wantReach, reached,
				"whether the request reached the MCP handler")
		})
	}
}

// TestProtectMCP_UnauthenticatedSaysUnauthorized pins the middleware ORDER.
//
// RequireMFA before RequireRole answers a request with no credentials at all
// with 403 "MFA verification required", which is both wrong and a confusing
// thing to debug — it implies the caller is authenticated and merely needs a
// TOTP code. ticketRouter applies RequireRole first; this must match.
func TestProtectMCP_UnauthenticatedSaysUnauthorized(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	rec := httptest.NewRecorder()

	h.srv.ProtectMCP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "unauthorized")
	require.NotContains(t, rec.Body.String(), "mfa",
		"an unauthenticated caller must not be told it is an MFA problem")
}
