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

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// mcpSession is a real MCP client over the SSE transport, authenticated with an
// OAuth bearer token: the whole path from the HTTP chain to the tool and the
// database, with nothing built by hand in between.
type mcpSession struct {
	t        *testing.T
	base     string
	token    string
	endpoint string
	events   chan string
	nextID   int
}

func newMCPSession(t *testing.T, h *harness, token string) *mcpSession {
	t.Helper()
	m := mcp.New(h.ticketSvc, nil, h.srv, h.categorySvc, h.srv)
	ts := httptest.NewServer(h.srv.ProtectMCP(m.Handler()))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() { _ = resp.Body.Close() })

	s := &mcpSession{t: t, base: ts.URL, token: token, events: make(chan string, 16)}
	endpoints := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data := strings.TrimPrefix(line, "data: ")
				if event == "endpoint" {
					endpoints <- data
				} else {
					s.events <- data
				}
			}
		}
	}()
	select {
	case s.endpoint = <-endpoints:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no endpoint event from the MCP server")
	}
	s.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "0"},
	})
	return s
}

// call sends one JSON-RPC request and returns its result object.
func (s *mcpSession) call(method string, params any) map[string]any {
	s.t.Helper()
	s.nextID++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params})
	require.NoError(s.t, err)
	url := s.endpoint
	if strings.HasPrefix(url, "/") {
		url = s.base + url
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(s.t, err)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.t, err)
	_ = resp.Body.Close()
	require.Less(s.t, resp.StatusCode, 300)

	select {
	case ev := <-s.events:
		var out struct {
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		require.NoError(s.t, json.Unmarshal([]byte(ev), &out))
		require.Nil(s.t, out.Error, "JSON-RPC error: %s", ev)
		return out.Result
	case <-time.After(5 * time.Second):
		require.FailNow(s.t, "no response to "+method)
	}
	return nil
}

func (s *mcpSession) tool(name string, args map[string]any) (text string, isError bool) {
	s.t.Helper()
	res := s.call("tools/call", map[string]any{"name": name, "arguments": args})
	content, _ := res["content"].([]any)
	require.NotEmpty(s.t, content)
	first, _ := content[0].(map[string]any)
	text, _ = first["text"].(string)
	isError, _ = res["isError"].(bool)
	return text, isError
}

// A public reply over MCP on a ticket a service user reported queues no mail:
// ReplyRecipient is the one rule for REST and MCP, and it must not hand a
// made-up .invalid address to the mailer (#420, #442).
func TestServiceAccount_NeverMailedOverMCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true

	reporter := h.newServiceClient(t, "Nagios", "tickets:read")
	replier := h.newServiceClient(t, "Zapier", "tickets:read", "tickets:write")
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Filed by a client", Description: "x", CategoryID: h.catID,
		Priority: ticket.PriorityLow, ReporterUserID: &reporter.UserID,
	})
	require.NoError(t, err)
	h.dispatcher.queued = nil

	text, isErr := newMCPSession(t, h, replier.Token).tool("add_reply", map[string]any{
		"ticket_id": tk.ID.String(), "body": "We are on it.",
	})
	require.False(t, isErr, "add_reply: %s", text)
	require.NotEmpty(t, h.dispatcher.queued)
	for _, ev := range h.dispatcher.queued {
		require.Empty(t, ev.Recipient, "event %s must carry no recipient address", ev.Type)
	}
}

// A bearer client reaches MCP and acts as its service user, under its scopes.
func TestServiceAccount_ActsAsItsUserOverMCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	writer := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")
	text, isErr := newMCPSession(t, h, writer.Token).tool("create_ticket", map[string]any{
		"subject": "Raised over MCP", "description": "x", "category_id": h.catID.String(),
	})
	require.False(t, isErr, "create_ticket: %s", text)

	// Find it by what the database says: its reporter is the client's user.
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all&q=Raised", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rows []struct {
		Subject        string `json:"subject"`
		ReporterUserID string `json:"reporter_user_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rows))
	require.Len(t, rows, 1)
	require.Equal(t, writer.UserID.String(), rows[0].ReporterUserID, "the reporter is the client's service user")
	_ = ctx

	// Scopes still gate it: a read-only client is refused the write tool.
	reader := h.newServiceClient(t, "Reader", "tickets:read")
	text, isErr = newMCPSession(t, h, reader.Token).tool("create_ticket", map[string]any{
		"subject": "Should not exist", "description": "x", "category_id": h.catID.String(),
	})
	require.True(t, isErr)
	require.Contains(t, text, "tickets:write")
}
