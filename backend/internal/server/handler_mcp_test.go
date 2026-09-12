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
// ProtectMCP applies the same chain as /api/. It authenticates every caller and
// refuses anyone who is not a signed-in user; what a given role may then DO is
// decided per tool in internal/mcp, not here. The sentinel handler below stands
// in for the MCP server: what matters is whether the request reaches it at all.
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
			// Reporting users reach MCP so they can read their own tickets.
			// The transport admits them; it does not decide what they may do.
			// Every tool gates its own writes on staff and filters its own
			// reads through the Authorizer — see internal/mcp's
			// TestWriteTools_RefuseReportingUsers and
			// TestBuildListFilter_CallerCannotWidenVisibility, which are the
			// checks that make widening here safe.
			name:       "a reporting user is admitted, and gated per tool",
			authHeader: "ApiKey " + h.userKey,
			wantStatus: http.StatusOK,
			wantReach:  true,
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
