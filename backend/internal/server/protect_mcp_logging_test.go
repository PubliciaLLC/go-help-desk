package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// /mcp/ is mounted on the bare ServeMux rather than the chi router, so it never
// reached the r.Use stack that logs every /api/ request. GHSA-5g72-m483-3v63
// named that gap next to the auth one; the auth half was fixed and this half
// was not, leaving the MCP tool surface with no request-log trace at all.
//
// That matters because GHSA-2x4f-j4jv-m2cm instructs operators to review recent
// activity for signs of exploitation. Against an unlogged surface they could not.
func TestProtectMCP_LogsRequests(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	handler := h.srv.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Unauthenticated: rejected by the auth chain before ever reaching the
	// handler. This is precisely the request an operator reviewing an incident
	// needs to see, so the log line must record the 401 the client actually got
	// rather than the 200 the inner handler would have written.
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)

	logged := buf.String()
	require.Contains(t, logged, "/mcp/sse", "MCP requests must appear in the request log")
	require.Contains(t, logged, "status=401", "the logged status must be what the client saw")

	// An authenticated request is logged too — otherwise only failures would be
	// visible, and successful exfiltration is the case that matters.
	buf.Reset()
	req = httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, buf.String(), "/mcp/sse")
	require.Contains(t, buf.String(), "status=200")
}

// Recoverer is part of the same chi stack. Without it a panic in an MCP tool
// takes down the process rather than returning 500 for the one request.
func TestProtectMCP_RecoversFromPanic(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	handler := h.srv.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rr := httptest.NewRecorder()

	require.NotPanics(t, func() { handler.ServeHTTP(rr, req) })
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

// The log line must not carry the credential. A request log that records the
// Authorization header turns an incident-review tool into a secret store.
func TestProtectMCP_LogDoesNotLeakTheAPIKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	handler := h.srv.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Positive assertion first: two NotContains checks both pass against an empty
	// buffer, so without this the test would keep passing if logging broke
	// entirely — which is the opposite of what it exists to guard.
	require.Contains(t, buf.String(), "/mcp/sse", "expected the request to be logged at all")

	require.NotContains(t, buf.String(), h.apiKey, "the raw API key must never reach the log")
	require.False(t, strings.Contains(strings.ToLower(buf.String()), "authorization"))
}
