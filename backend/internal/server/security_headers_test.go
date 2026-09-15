package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// None of these were set. The admin pages could be framed by another site and
// clicked through, and a file with a guessable type could be sniffed into
// something executable.
func TestSecurityHeaders_AreSet(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
	require.Equal(t, "strict-origin-when-cross-origin", rr.Header().Get("Referrer-Policy"))

	csp := rr.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	for _, directive := range []string{
		"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'",
	} {
		require.Contains(t, csp, directive)
	}
	// The frontend bundle has no inline scripts and no eval, so the policy must
	// not weaken script-src. If someone adds an inline script later, the right
	// fix is a nonce, not 'unsafe-inline' here.
	require.NotContains(t, csp, "script-src 'self' 'unsafe-inline'")
	require.NotContains(t, csp, "unsafe-eval")
}

// A handler that chooses its own policy keeps it. The logo route serves a file
// that needs a stricter one than the site.
func TestSecurityHeaders_DoNotOverrideAHandlersOwnPolicy(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", logoCSP)
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/logo", nil))

	require.Equal(t, logoCSP, rr.Header().Get("Content-Security-Policy"))
	require.Contains(t, logoCSP, "sandbox")
	require.Contains(t, logoCSP, "script-src 'none'")
}
