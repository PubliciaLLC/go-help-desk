package server_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// Every other ProtectMCP test wraps a sentinel http.HandlerFunc, which tells us
// nothing about the transport that actually runs behind it. mcp-go's SSE handler
// does a bare `w.(http.Flusher)` assertion and answers 500 "Streaming
// unsupported" when it fails — so a middleware whose ResponseWriter wrapper does
// not forward Flusher silently kills MCP while every sentinel-based test stays
// green. That is exactly what happened when requestLogger was added here.
//
// This test puts the real SSE server behind ProtectMCP.
func TestProtectMCP_RealSSETransport_Streams(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	prefix := func(context.Context) string { return "GHD" }
	mcpSrv := mcp.New(h.ticketSvc, prefix, h.srv, h.categorySvc)
	handler := h.srv.ProtectMCP(mcpSrv.Handler())

	ts := httptest.NewServer(handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"SSE must not 500 — a 500 here is 'Streaming unsupported', meaning a "+
			"middleware wrapper swallowed http.Flusher and MCP is dead")

	// A status alone would pass even if nothing ever streamed. The endpoint event
	// is the first thing mcp-go writes and flushes, so reading it proves the
	// flush reached the client rather than sitting in a buffer.
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "event:"),
		"expected an SSE event as the first line, got %q", line)
}

// The property the transport actually depends on, asserted directly so the
// reason for it is legible without knowing mcp-go's internals.
func TestProtectMCP_PreservesFlusher(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	var isFlusher bool
	handler := h.srv.ProtectMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, isFlusher = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, isFlusher,
		"the ResponseWriter reaching the MCP handler must still implement http.Flusher")
}
