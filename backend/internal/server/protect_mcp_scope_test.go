package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// Drives the REAL registered MCP tools over the REAL SSE transport behind
// ProtectMCP, with a restricted API key.
//
// This is the test that was missing. Every other ProtectMCP test uses
// h.apiKey, which holds every scope, so nothing could have caught that MCP read
// no scopes at all: a credential with none could create tickets and read every
// thread through MCP while getting 403 on the REST equivalents.
//
// It also catches a tool registered without its scope wrapper, which a test
// that wraps handlers itself cannot — but only for the tools it actually calls.
// The first version of this file drove three of the eight, so unwrapping
// add_reply or assign_ticket survived the whole suite. Every tool is exercised
// below; mcpTools is the single list, so a tool added without an entry here is
// the thing to notice in review.
type mcpConn struct {
	ts         *httptest.Server
	token      string
	messageURL string
	events     *bufio.Reader
	closeBody  func()
}

func openMCP(t *testing.T, h *harness, token string) *mcpConn {
	t.Helper()
	mcpSrv := mcp.New(h.ticketSvc, func(context.Context) string { return "GHD" }, h.srv, h.categorySvc)
	ts := httptest.NewServer(h.srv.ProtectMCP(mcpSrv.Handler()))
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "ApiKey "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "SSE must open")

	br := bufio.NewReader(resp.Body)
	// First event carries the message endpoint, including the session id.
	var endpoint string
	for i := 0; i < 10 && endpoint == ""; i++ {
		line, err := br.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			endpoint = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	require.NotEmpty(t, endpoint, "expected an endpoint event")

	return &mcpConn{ts: ts, token: token, messageURL: ts.URL + endpoint,
		events: br, closeBody: func() { _ = resp.Body.Close() }}
}

// call sends one tools/call and returns the text of the result.
func (c *mcpConn) call(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, c.messageURL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "ApiKey "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "the message POST itself must be accepted")

	// The reply arrives on the SSE stream.
	done := make(chan string, 1)
	go func() {
		for {
			line, err := c.events.ReadString('\n')
			if err != nil {
				done <- ""
				return
			}
			if strings.HasPrefix(line, "data: ") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				if strings.Contains(payload, `"result"`) || strings.Contains(payload, `"error"`) {
					done <- payload
					return
				}
			}
		}
	}()

	select {
	case got := <-done:
		require.NotEmpty(t, got, "expected a JSON-RPC reply on the stream")
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the MCP reply")
		return ""
	}
}

// mcpTools is every registered tool and the scope it requires. Arguments only
// have to be well-formed enough to reach the handler: the scope check runs
// before argument validation, so a random UUID is sufficient.
func mcpTools(h *harness) []struct {
	tool  string
	write bool
	args  map[string]any
} {
	tid := uuid.New().String()
	return []struct {
		tool  string
		write bool
		args  map[string]any
	}{
		{"get_ticket", false, map[string]any{"id": tid}},
		{"list_tickets", false, map[string]any{}},
		{"list_categories", false, map[string]any{}},
		{"list_statuses", false, map[string]any{}},
		{"create_ticket", true, map[string]any{"subject": "x", "category_id": h.catID.String()}},
		{"add_reply", true, map[string]any{"ticket_id": tid, "body": "x"}},
		{"assign_ticket", true, map[string]any{"ticket_id": tid, "assignee_user_id": uuid.New().String()}},
		{"update_ticket_status", true, map[string]any{"ticket_id": tid, "status": "Resolved"}},
	}
}

func TestMCP_RestrictedKeyIsRefusedOverTheRealTransport(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("no scopes reaches no tool", func(t *testing.T) {
		c := openMCP(t, h, mintLegacyKey(t, h))
		defer c.closeBody()

		for _, tc := range mcpTools(h) {
			t.Run(tc.tool, func(t *testing.T) {
				got := c.call(t, tc.tool, tc.args)
				require.Contains(t, got, "scope",
					"%s must refuse a credential with no scopes; got %s", tc.tool, got)
			})
		}
	})

	t.Run("read scope cannot write", func(t *testing.T) {
		c := openMCP(t, h, mintKey(t, h, []string{"tickets:read"}))
		defer c.closeBody()

		for _, tc := range mcpTools(h) {
			t.Run(tc.tool, func(t *testing.T) {
				got := c.call(t, tc.tool, tc.args)
				if tc.write {
					require.Contains(t, got, "scope",
						"tickets:read must not reach the write tool %s; got %s", tc.tool, got)
					return
				}
				require.NotContains(t, got, "does not carry",
					"tickets:read must reach the read tool %s; got %s", tc.tool, got)
			})
		}
	})

	t.Run("write scope reaches the write tools", func(t *testing.T) {
		c := openMCP(t, h, mintKey(t, h, []string{"tickets:write"}))
		defer c.closeBody()

		for _, tc := range mcpTools(h) {
			if !tc.write {
				continue
			}
			t.Run(tc.tool, func(t *testing.T) {
				got := c.call(t, tc.tool, tc.args)
				require.NotContains(t, got, "does not carry",
					"tickets:write must reach %s; got %s", tc.tool, got)
			})
		}
	})
}
