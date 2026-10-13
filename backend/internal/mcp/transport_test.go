package mcp

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests pin which transport answers which path. Handler() serves two:
// Streamable HTTP at /mcp (and /mcp/), HTTP+SSE at /mcp/sse and /mcp/message.
// They run without a mux and without ProtectMCP on purpose: the dispatch lives
// in Handler(), so every test that wraps Handler() in httptest.NewServer
// exercises the real routing.
//
// s.tickets is nil. No case reaches a service, so a nil one is the strongest
// assertion that none is touched.

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func TestHandler_RoutesBothTransports(t *testing.T) {
	cases := []struct {
		name         string
		method, path string
		body         string
		wantStatus   int
		wantType     string // prefix of Content-Type; "" = not checked
		wantBody     string // substring; "" = not checked
	}{
		{
			name:   "initialize at /mcp",
			method: http.MethodPost, path: "/mcp", body: initializeBody,
			wantStatus: http.StatusOK, wantType: "application/json", wantBody: `"go-help-desk"`,
		},
		{
			// The 1.2 documentation gave /mcp/, so configurations copied from
			// it must keep connecting.
			name:   "initialize at /mcp/",
			method: http.MethodPost, path: "/mcp/", body: initializeBody,
			wantStatus: http.StatusOK, wantType: "application/json", wantBody: `"go-help-desk"`,
		},
		{
			// The server never sends anything unprompted, so it offers no
			// event stream; the spec allows 405 for that.
			name:   "GET /mcp is not a stream",
			method: http.MethodGet, path: "/mcp",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:   "unknown subpath",
			method: http.MethodPost, path: "/mcp/nope", body: initializeBody,
			wantStatus: http.StatusNotFound,
		},
		{
			// Without ProtectMCP there is no actor in the context. The tool
			// must say so rather than act as anybody.
			name:   "no actor, no fabrication",
			method: http.MethodPost, path: "/mcp",
			body:       `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_categories","arguments":{}}}`,
			wantStatus: http.StatusOK, wantBody: noActorMessage,
		},
	}

	s := New(nil, nil, nil, nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
			require.NoError(t, err)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.Equal(t, tc.wantStatus, resp.StatusCode, "body: %s", body)
			if tc.wantType != "" {
				require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), tc.wantType),
					"Content-Type %q", resp.Header.Get("Content-Type"))
			}
			if tc.wantBody != "" {
				require.Contains(t, string(body), tc.wantBody)
			}
			if tc.path == "/mcp" || tc.path == "/mcp/" {
				// Stateless on purpose: see docs/DESIGN.md, "MCP Interface".
				require.Empty(t, resp.Header.Get("Mcp-Session-Id"),
					"a session id means the server went stateful")
			}
		})
	}
}

// SSE is kept for existing client configurations: it must still stream from
// /mcp/sse after the Streamable HTTP transport is added.
func TestHandler_SSEStillAtMCPSSE(t *testing.T) {
	s := New(nil, nil, nil, nil)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp/sse", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"),
		"Content-Type %q", resp.Header.Get("Content-Type"))
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "event:"), "first line %q", line)
}
