package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// Drives the REAL registered MCP tools over the REAL Streamable HTTP transport
// at /mcp, behind ProtectMCP. protect_mcp_scope_test.go does the same for SSE,
// and runs its scope assertions over both transports.
//
// The Streamable HTTP endpoint is stateless on purpose (docs/DESIGN.md, "MCP
// Interface"): every POST is one complete JSON-RPC exchange answered with
// application/json, no Mcp-Session-Id is issued, and GET is 405.

// streamConn is a client of the /mcp endpoint. It holds no connection: there is
// no stream to hold.
type streamConn struct {
	base    string // the test server's URL, without a path
	url     string // base + "/mcp"
	header  http.Header
	cookies []*http.Cookie
}

func serveMCP(t *testing.T, h *harness) string {
	t.Helper()
	mcpSrv := mcp.New(h.ticketSvc, func(context.Context) string { return "GHD" }, h.srv, h.categorySvc, h.srv)
	ts := httptest.NewServer(h.srv.ProtectMCP(mcpSrv.Handler()))
	t.Cleanup(ts.Close)
	return ts.URL
}

// openStream returns a client for /mcp carrying an API key; "" carries none.
func openStream(t *testing.T, h *harness, token string) *streamConn {
	t.Helper()
	base := serveMCP(t, h)
	c := &streamConn{base: base, url: base + "/mcp", header: http.Header{}}
	if token != "" {
		c.header.Set("Authorization", "ApiKey "+token)
	}
	return c
}

// openStreamWithCookies returns a client for /mcp carrying session cookies.
func openStreamWithCookies(t *testing.T, h *harness, cookies []*http.Cookie) *streamConn {
	t.Helper()
	base := serveMCP(t, h)
	return &streamConn{base: base, url: base + "/mcp", header: http.Header{}, cookies: cookies}
}

// postTo sends one JSON-RPC message to url and returns the response.
func (c *streamConn) postTo(t *testing.T, url string, payload map[string]any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return c.send(t, http.MethodPost, url, "application/json", body)
}

func (c *streamConn) post(t *testing.T, payload map[string]any) *http.Response {
	t.Helper()
	return c.postTo(t, c.url, payload)
}

// send is the raw request, for the cases that are not well-formed JSON-RPC.
func (c *streamConn) send(t *testing.T, method, url, contentType string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	require.NoError(t, err)
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// call sends one tools/call and returns the JSON-RPC reply. Same shape as
// (*mcpConn).call, which does it over SSE.
func (c *streamConn) call(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	resp := c.post(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	got := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", got)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	return got
}

func (c *streamConn) rpc(t *testing.T, method string, params map[string]any) (*http.Response, string) {
	t.Helper()
	resp := c.post(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return resp, readBody(t, resp)
}

var initializeParams = map[string]any{
	"protocolVersion": "2025-06-18",
	"capabilities":    map[string]any{},
	"clientInfo":      map[string]any{"name": "t", "version": "1"},
}

// toolText returns the first text content of a tools/call reply, whether it
// came as a plain JSON body or as the data of an SSE event.
func toolText(t *testing.T, payload string) string {
	t.Helper()
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload: %s", payload)
	require.NotEmpty(t, out.Result.Content, "payload: %s", payload)
	return out.Result.Content[0].Text
}

func TestMCPStreamable_InitializeListAndCall(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := openStream(t, h, h.apiKey)

	resp, body := c.rpc(t, "initialize", initializeParams)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	require.Contains(t, body, `"go-help-desk"`)
	// Stateless on purpose, see docs/DESIGN.md: a session id would mean the
	// server keeps every initialized session in memory.
	require.Empty(t, resp.Header.Get("Mcp-Session-Id"))

	resp, body = c.rpc(t, "tools/list", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	var got, want []string
	for _, tl := range list.Result.Tools {
		got = append(got, tl.Name)
	}
	for _, tc := range mcpTools(h) {
		want = append(want, tc.tool)
	}
	sort.Strings(got)
	sort.Strings(want)
	require.Equal(t, want, got, "the same tools as over SSE")

	reply := c.call(t, "list_statuses", map[string]any{})
	var res struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(reply), &res))
	require.False(t, res.Result.IsError, reply)
	require.NotEmpty(t, toolText(t, reply))

	// The 1.2 documentation gave /mcp/, so it answers too.
	resp = c.postTo(t, c.base+"/mcp/", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams,
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
}

// The new path must refuse an anonymous caller exactly as /mcp/sse does, with
// the same status and body — and say "unauthorized", not "mfa".
func TestMCPStreamable_UnauthenticatedMatchesSSE(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for name, token := range map[string]string{
		"no credential": "",
		"garbage key":   "not-a-real-key",
	} {
		t.Run(name, func(t *testing.T) {
			c := openStream(t, h, token)

			resp := c.post(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams})
			streamBody := readBody(t, resp)
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, streamBody)

			sse := c.send(t, http.MethodGet, c.base+"/mcp/sse", "", nil)
			sseBody := readBody(t, sse)
			require.Equal(t, http.StatusUnauthorized, sse.StatusCode)

			require.Equal(t, sseBody, streamBody)
			require.Contains(t, streamBody, "unauthorized")
			require.NotContains(t, streamBody, "mfa")
		})
	}
}

// RequireMFA is part of the chain both transports share: a session that has
// passed its password but owes a second factor must not reach MCP.
func TestMCPStreamable_MFAPendingSessionRefused(t *testing.T) {
	t.Run("password only", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		mfaProtectedStaff(t, h)

		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
			"email": "staff@test.local", "password": "password",
		})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var login map[string]any
		require.NoError(t, json.Unmarshal(body, &login))
		require.True(t, login["mfa_needed"].(bool), "precondition: the account is MFA-protected")
		require.NotEmpty(t, s.jar, "precondition: the login set a session cookie")

		c := openStreamWithCookies(t, h, s.jar)
		resp := c.post(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams})
		got := readBody(t, resp)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, got)
		require.Contains(t, got, "mfa_required")

		sse := c.send(t, http.MethodGet, c.base+"/mcp/sse", "", nil)
		require.Equal(t, http.StatusForbidden, sse.StatusCode, "SSE is behind the same gate")
	})

	// Without this the test above could pass because cookies never work on the
	// new path at all.
	t.Run("positive control: MFA off, the cookie works", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()

		s := &session{h: h}
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
			"email": "staff@test.local", "password": "password",
		})
		require.Equal(t, http.StatusOK, res.StatusCode)

		c := openStreamWithCookies(t, h, s.jar)
		reply := c.call(t, "list_statuses", map[string]any{})
		require.NotEmpty(t, toolText(t, reply))
	})
}

func TestMCPStreamable_ReportingUserCannotWrite(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := openStream(t, h, h.userKey)

	reply := c.call(t, "create_ticket", map[string]any{"subject": "x", "category_id": h.catID.String()})
	require.Equal(t, "this tool requires a staff or administrator account", toolText(t, reply))

	reply = c.call(t, "list_tickets", map[string]any{})
	var res struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(reply), &res))
	require.False(t, res.Result.IsError, reply)
}

// The acting identity is taken from each HTTP request, never from an earlier
// initialize or from a session id the client sends. An admin initializes; the
// next request carries a reporting user's key and must act as that user; the
// one after carries the staff key and must act as staff.
func TestMCPStreamable_ActorIsTakenFromEachRequest(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)
	as := func(token string) *streamConn {
		c := &streamConn{base: base, url: base + "/mcp", header: http.Header{}}
		c.header.Set("Authorization", "ApiKey "+token)
		return c
	}
	args := map[string]any{"subject": "who am i", "category_id": h.catID.String()}

	admin := as(h.adminKey)
	resp, body := admin.rpc(t, "initialize", initializeParams)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)

	reporting := as(h.userKey)
	// A well-formed session id, as a client that was handed one would send.
	reporting.header.Set("Mcp-Session-Id", "mcp-session-"+uuid.New().String())
	require.Equal(t, "this tool requires a staff or administrator account",
		toolText(t, reporting.call(t, "create_ticket", args)))

	staff := as(h.apiKey)
	staff.header.Set("Mcp-Session-Id", reporting.header.Get("Mcp-Session-Id"))
	var created ticket.Ticket
	require.NoError(t, json.Unmarshal([]byte(toolText(t, staff.call(t, "create_ticket", args))), &created))
	require.NotNil(t, created.ReporterUserID)
	require.Equal(t, h.staffID, *created.ReporterUserID, "the ticket is the staff user's, not the admin's")
}

// A ticket the caller may not see answers exactly as a ticket that does not
// exist, on both transports.
func TestMCPStreamable_HiddenTicketAnswersLikeSSE(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "hidden", Description: "x", CategoryID: h.catID, ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	want := "not found: ticket " + tk.ID.String()
	viaStream := toolText(t, openStream(t, h, h.userKey).call(t, "get_ticket", map[string]any{"id": tk.ID.String()}))

	sse := openMCP(t, h, h.userKey)
	defer sse.closeBody()
	viaSSE := toolText(t, sse.call(t, "get_ticket", map[string]any{"id": tk.ID.String()}))

	require.Equal(t, want, viaStream)
	require.Equal(t, want, viaSSE)

	missing := uuid.New().String()
	require.Equal(t, "not found: ticket "+missing,
		toolText(t, openStream(t, h, h.userKey).call(t, "get_ticket", map[string]any{"id": missing})))
}

// Pins the cross-site surface of /mcp (docs/DESIGN.md, "MCP Interface" ->
// Origin). The MCP specification asks servers to validate Origin against DNS
// rebinding, and ProtectMCP does, on every MCP route: a request whose Origin is
// not BASE_URL's gets 403 before authentication (protect_mcp_origin_test.go
// tests that check in full).
//
// That check is the first of several independent reasons a cross-site page
// cannot make a browser run a tool, and the rest are pinned here so that none
// of them quietly becomes the only one:
//
//   - the session cookie is SameSite=Lax, so it is not sent on a cross-site
//     POST (internal/database/sessionstore/cookie_attributes_test.go);
//   - a non-JSON POST, the only kind a page can send without a preflight, is
//     refused before anything runs;
//   - a JSON POST needs a CORS preflight, and /mcp never grants one: with the
//     Origin check out of the way (the right Origin) it still sends no CORS
//     headers;
//   - GET is 405, so there is no stream a navigation could hold open.
//
// If one of these stops being true, the Origin check is what is left; do not
// remove it. They are properties of /mcp alone: on /mcp/message a text/plain
// POST runs, so there the Origin check is the only barrier
// (TestMCPOrigin_SSEMessageCheckedWhereNothingElseStopsIt).
func TestMCPStreamable_NoCrossSiteSurface(t *testing.T) {
	h, cleanup := newIsolatedHarness(t)
	defer cleanup()
	c := openStream(t, h, h.apiKey)

	t.Run("a non-JSON POST runs nothing", func(t *testing.T) {
		before := latestTicketNumber(t, h)
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "create_ticket", "arguments": map[string]any{
				"subject": "cross-site", "category_id": h.catID.String(),
			}},
		})
		require.NoError(t, err)
		resp := c.send(t, http.MethodPost, c.url, "text/plain", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, readBody(t, resp))
		after := latestTicketNumber(t, h)
		require.Equal(t, before+1, after, "only the probe may have taken a ticket number")
	})

	t.Run("a non-JSON POST from another origin runs nothing either", func(t *testing.T) {
		before := latestTicketNumber(t, h)
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_ticket","arguments":{"subject":"cross-site","category_id":"` + h.catID.String() + `"}}}`)
		cross := withOrigin(&streamConn{base: c.base, url: c.url, header: c.header.Clone()}, foreignOrigin)
		requireOriginRefused(t, cross.send(t, http.MethodPost, c.url, "text/plain", body))
		require.Equal(t, before+1, latestTicketNumber(t, h), "only the probe may have taken a ticket number")
	})

	t.Run("no CORS preflight is granted", func(t *testing.T) {
		c.header.Set("Origin", "https://evil.example")
		c.header.Set("Access-Control-Request-Method", "POST")
		c.header.Set("Access-Control-Request-Headers", "authorization,content-type")
		defer func() {
			c.header.Del("Origin")
			c.header.Del("Access-Control-Request-Method")
			c.header.Del("Access-Control-Request-Headers")
		}()
		resp := c.send(t, http.MethodOptions, c.url, "", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "the Origin check answers a foreign preflight")
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"))
	})

	t.Run("the endpoint itself grants no CORS even to the right origin", func(t *testing.T) {
		// What stops a same-site sibling page, or anything that gets past the
		// Origin check, from sending a JSON POST: the preflight is never
		// answered with permission.
		own := withOrigin(&streamConn{base: c.base, url: c.url, header: c.header.Clone()}, baseOrigin)
		own.header.Set("Access-Control-Request-Method", "POST")
		own.header.Set("Access-Control-Request-Headers", "authorization,content-type")
		resp := own.send(t, http.MethodOptions, c.url, "", nil)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"))
	})

	t.Run("GET is not a stream", func(t *testing.T) {
		resp := c.send(t, http.MethodGet, c.url, "", nil)
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("responses carry no CORS header", func(t *testing.T) {
		resp, _ := c.rpc(t, "initialize", initializeParams)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))

		own := withOrigin(&streamConn{base: c.base, url: c.url, header: c.header.Clone()}, baseOrigin)
		resp, _ = own.rpc(t, "initialize", initializeParams)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), "not even for the instance's own origin")
	})
}
