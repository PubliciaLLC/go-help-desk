package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// The pages that actually run script carry the headers too.
//
// The SPA and /mcp/ are mounted on the bare ServeMux, outside the chi chain
// that sets these — so the JSON endpoints had a Content-Security-Policy and
// every HTML page in the application did not. That is the wrong way round.
// CSP on a JSON response does very little; on the HTML it is the backstop for
// the injected script the rest of this codebase works to prevent, and
// X-Frame-Options on the admin screens is what stops another site framing
// them.
//
// DESIGN.md says "security headers on every response". These were the two
// places it was not true.
func TestSPAHandler_CarriesTheSecurityHeaders(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<!doctype html><title>x</title>")},
		"assets/app.js":  &fstest.MapFile{Data: []byte("export {}")},
		"assets/app.css": &fstest.MapFile{Data: []byte("body{}")},
	}
	h := server.NewSPAHandler(ui)

	for _, path := range []string{
		"/",               // index
		"/admin/users",    // a client-side route, served index.html
		"/assets/app.js",  // a real file
		"/does/not/exist", // the fallback
	} {
		t.Run(path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
			res := rr.Result()
			defer res.Body.Close()

			require.NotEmpty(t, res.Header.Get("Content-Security-Policy"),
				"no CSP, so an injected script has no backstop on the page that runs scripts")
			require.Equal(t, "DENY", res.Header.Get("X-Frame-Options"),
				"the admin screens could be framed by another site")
			require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
			require.NotEmpty(t, res.Header.Get("Referrer-Policy"))
		})
	}
}

// And the MCP mount, which is on the same bare mux for the same reason.
func TestProtectMCP_CarriesTheSecurityHeaders(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	handler := h.srv.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated: the headers must be on the refusal too. A 401 is still
	// a response a browser renders.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp/", nil))
	res := rr.Result()
	defer res.Body.Close()

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("Content-Security-Policy"))
	require.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
}
