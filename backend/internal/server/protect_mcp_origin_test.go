package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The Origin check on the Streamable HTTP endpoint (docs/DESIGN.md, "MCP
// Interface" -> Origin). The MCP specification requires servers to validate
// Origin against DNS rebinding; mcp-go does not, so ProtectMCP does:
//
//   - on every route behind ProtectMCP: /mcp, /mcp/, /mcp/sse and
//     /mcp/message (see TestMCPOrigin_SSEIsChecked);
//   - a request with no Origin passes, because non-browser clients send none;
//   - an Origin must be the one origin of BASE_URL, or the request is 403
//     origin_not_allowed;
//   - the refusal comes BEFORE authentication, so it says nothing about
//     whether the credential beside it was any good.
//
// The harness's BASE_URL is http://localhost:8080.
const (
	baseOrigin    = "http://localhost:8080"
	foreignOrigin = "https://evil.example"
)

// withOrigin sets the Origin header on c, one value per argument.
func withOrigin(c *streamConn, origins ...string) *streamConn {
	c.header.Del("Origin")
	for _, o := range origins {
		c.header.Add("Origin", o)
	}
	return c
}

// requireOriginRefused asserts the refusal and returns its body. The body is
// the repo's standard error envelope, and it does not repeat BASE_URL: telling
// a foreign page what the right origin is would help nobody.
func requireOriginRefused(t *testing.T, resp *http.Response) string {
	t.Helper()
	body := readBody(t, resp)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, body)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &env), body)
	require.Equal(t, "origin_not_allowed", env.Error.Code, body)
	require.NotEmpty(t, env.Error.Message)
	require.NotContains(t, body, "localhost")
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), "security headers are on every MCP response")
	return body
}

func TestMCPOrigin_MatchingOriginWorks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)

	for _, path := range []string{"/mcp", "/mcp/"} {
		for name, origin := range map[string]string{
			"as BASE_URL is written": baseOrigin,
			"upper case":             "HTTP://LOCALHOST:8080",
		} {
			t.Run(path+" "+name, func(t *testing.T) {
				c := withOrigin(&streamConn{base: base, url: base + path, header: http.Header{}}, origin)
				c.header.Set("Authorization", "ApiKey "+h.apiKey)

				resp, body := c.rpc(t, "initialize", initializeParams)
				require.Equal(t, http.StatusOK, resp.StatusCode, body)
				require.NotEmpty(t, toolText(t, c.call(t, "list_statuses", map[string]any{})))
			})
		}
	}

	t.Run("a signed-in browser session, the case the check exists for", func(t *testing.T) {
		s := &session{h: h}
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
			"email": "staff@test.local", "password": "password",
		})
		require.Equal(t, http.StatusOK, res.StatusCode)

		c := withOrigin(&streamConn{base: base, url: base + "/mcp", header: http.Header{}, cookies: s.jar}, baseOrigin)
		require.NotEmpty(t, toolText(t, c.call(t, "list_statuses", map[string]any{})))

		requireOriginRefused(t, withOrigin(c, foreignOrigin).post(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams,
		}))
	})
}

func TestMCPOrigin_NoOriginWorks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, path := range []string{"/mcp", "/mcp/"} {
		t.Run(path, func(t *testing.T) {
			base := serveMCP(t, h)
			c := &streamConn{base: base, url: base + path, header: http.Header{}}
			c.header.Set("Authorization", "ApiKey "+h.apiKey)
			_, present := c.header["Origin"]
			require.False(t, present, "precondition: this client sends no Origin")

			resp, body := c.rpc(t, "initialize", initializeParams)
			require.Equal(t, http.StatusOK, resp.StatusCode, body)
		})
	}
}

func TestMCPOrigin_ForeignOriginRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name    string
		origins []string
	}{
		{"another site", []string{foreignOrigin}},
		{"null, from a sandboxed frame or file://", []string{"null"}},
		{"right host, wrong port", []string{"http://localhost:9999"}},
		{"right host, no port", []string{"http://localhost"}},
		{"right host and port, wrong scheme", []string{"https://localhost:8080"}},
		{"loopback address instead of the name", []string{"http://127.0.0.1:8080"}},
		{"trailing slash", []string{baseOrigin + "/"}},
		{"userinfo", []string{"http://user@localhost:8080"}},
		{"suffix", []string{baseOrigin + ".evil.example"}},
		{"empty", []string{""}},
		{"two headers, both right", []string{baseOrigin, baseOrigin}},
		{"two headers, one foreign", []string{baseOrigin, foreignOrigin}},
	}
	for _, path := range []string{"/mcp", "/mcp/"} {
		for _, tc := range cases {
			t.Run(path+" "+tc.name, func(t *testing.T) {
				base := serveMCP(t, h)
				c := withOrigin(&streamConn{base: base, url: base + path, header: http.Header{}}, tc.origins...)
				c.header.Set("Authorization", "ApiKey "+h.apiKey)
				if tc.name == "empty" {
					// Add("") keeps the header present with an empty value.
					_, present := c.header["Origin"]
					require.True(t, present)
				}

				requireOriginRefused(t, c.post(t, map[string]any{
					"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams,
				}))
			})
		}
	}

	// A refusal runs nothing. A valid key and a well-formed create_ticket
	// would otherwise take a ticket number.
	t.Run("the tool does not run", func(t *testing.T) {
		before := latestTicketNumber(t, h)
		base := serveMCP(t, h)
		c := withOrigin(&streamConn{base: base, url: base + "/mcp", header: http.Header{}}, foreignOrigin)
		c.header.Set("Authorization", "ApiKey "+h.apiKey)
		requireOriginRefused(t, c.post(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "create_ticket", "arguments": map[string]any{
				"subject": "from a foreign page", "category_id": h.catID.String(),
			}},
		}))
		require.Equal(t, before+1, latestTicketNumber(t, h), "only the probe may have taken a ticket number")
	})
}

// The order, pinned: the Origin check runs BEFORE authentication. A foreign
// Origin is refused with the same status and the same body whatever credential
// is beside it, so the refusal tells a caller nothing about whether a key is
// valid, whether a session owes MFA, or what role it has. The controls without
// an Origin show the responses it is being kept from.
func TestMCPOrigin_RefusalComesBeforeAuthentication(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)
	initialize := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": initializeParams}

	mfa := &session{h: h}
	var pending []*http.Cookie
	t.Run("set up an MFA-pending session", func(t *testing.T) {
		mfaProtectedStaff(t, h)
		res, body := mfa.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
			"email": "staff@test.local", "password": "password",
		})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var login map[string]any
		require.NoError(t, json.Unmarshal(body, &login))
		require.True(t, login["mfa_needed"].(bool))
		pending = mfa.jar
	})

	callers := []struct {
		name    string
		token   string
		cookies []*http.Cookie
		// What the same caller gets with no Origin: the thing the Origin
		// check must not reveal.
		plainStatus int
		plainBody   string
	}{
		{"valid staff key", h.apiKey, nil, http.StatusOK, ""},
		{"valid reporting-user key", h.userKey, nil, http.StatusOK, ""},
		{"garbage key", "not-a-real-key", nil, http.StatusUnauthorized, "unauthorized"},
		{"no credential", "", nil, http.StatusUnauthorized, "unauthorized"},
		{"session owing MFA", "", pending, http.StatusForbidden, "mfa_required"},
	}

	var refusals []string
	for _, tc := range callers {
		mk := func() *streamConn {
			c := &streamConn{base: base, url: base + "/mcp", header: http.Header{}, cookies: tc.cookies}
			if tc.token != "" {
				c.header.Set("Authorization", "ApiKey "+tc.token)
			}
			return c
		}
		t.Run(tc.name, func(t *testing.T) {
			plain := mk().post(t, initialize)
			plainBody := readBody(t, plain)
			require.Equal(t, tc.plainStatus, plain.StatusCode, "control: "+plainBody)
			require.Contains(t, plainBody, tc.plainBody, "control")

			refusals = append(refusals, requireOriginRefused(t, withOrigin(mk(), foreignOrigin).post(t, initialize)))
		})
	}
	require.Len(t, refusals, len(callers))
	for _, body := range refusals[1:] {
		require.Equal(t, refusals[0], body, "the refusal must be identical for every credential")
	}
}

// A refusal is a response the client saw, so the request log records it with
// that status. The check sits inside the logger for exactly this reason: an
// operator reviewing activity (GHSA-2x4f-j4jv-m2cm) should see the foreign
// pages that tried.
func TestMCPOrigin_RefusalIsLogged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	reached := false
	handler := h.srv.ProtectMCP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Origin", foreignOrigin)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	requireOriginRefused(t, rec.Result())
	require.False(t, reached)
	require.Contains(t, buf.String(), "path=/mcp")
	require.Contains(t, buf.String(), "status=403")
	require.NotContains(t, buf.String(), h.apiKey, "the key does not reach the log")
}

// GET, DELETE and OPTIONS on /mcp are as they were for a caller with no Origin
// or the right one; only a foreign Origin changes, and it is refused before it
// reaches the transport, so a preflight is not answered either.
func TestMCPOrigin_OtherMethods(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)

	methods := []struct {
		method string
		want   int // for a caller the check lets through
	}{
		{http.MethodGet, http.StatusMethodNotAllowed},
		{http.MethodDelete, http.StatusMethodNotAllowed},
		{http.MethodOptions, http.StatusNotFound},
	}
	for _, m := range methods {
		mk := func(origin string) *streamConn {
			c := &streamConn{base: base, url: base + "/mcp", header: http.Header{}}
			c.header.Set("Authorization", "ApiKey "+h.apiKey)
			if origin != "" {
				withOrigin(c, origin)
			}
			return c
		}
		t.Run(m.method+" without Origin", func(t *testing.T) {
			resp := mk("").send(t, m.method, base+"/mcp", "", nil)
			require.Equal(t, m.want, resp.StatusCode)
		})
		t.Run(m.method+" with the right Origin", func(t *testing.T) {
			resp := mk(baseOrigin).send(t, m.method, base+"/mcp", "", nil)
			require.Equal(t, m.want, resp.StatusCode)
		})
		t.Run(m.method+" with a foreign Origin", func(t *testing.T) {
			c := mk(foreignOrigin)
			if m.method == http.MethodOptions {
				c.header.Set("Access-Control-Request-Method", "POST")
				c.header.Set("Access-Control-Request-Headers", "authorization,content-type")
			}
			resp := c.send(t, m.method, base+"/mcp", "", nil)
			requireOriginRefused(t, resp)
			require.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
			require.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"))
		})
	}

	t.Run("an anonymous preflight is still 401 without an Origin", func(t *testing.T) {
		c := &streamConn{base: base, url: base + "/mcp", header: http.Header{}}
		resp := c.send(t, http.MethodOptions, base+"/mcp", "", nil)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// The decision, pinned: the Origin check covers every route ProtectMCP guards,
// the legacy SSE transport (/mcp/sse, /mcp/message) included. docs/DESIGN.md
// says why. If this test is changed to let a foreign Origin through on SSE,
// that is a decision to leave a route open, not a fix.
func TestMCPOrigin_SSEIsChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)

	mk := func(origin, auth string) *streamConn {
		c := &streamConn{base: base, header: http.Header{}}
		if origin != "" {
			withOrigin(c, origin)
		}
		if auth != "" {
			c.header.Set("Authorization", auth)
		}
		return c
	}
	good := "ApiKey " + h.apiKey

	// refused sends one request and asserts the origin refusal, returning its
	// body. A route that wrongly lets the request through can hold the
	// response open (the SSE stream does), so the status is checked before
	// the body is read and the whole call is bounded: a regression fails here
	// instead of hanging the suite.
	refused := func(t *testing.T, c *streamConn, method, path string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(`{}`))
		require.NoError(t, err)
		for k, vs := range c.header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s %s was not refused for its Origin", method, path)
		return requireOriginRefused(t, resp)
	}

	// What /mcp says to a foreign Origin is the reference: the other routes
	// must say exactly that, to every caller.
	want := requireOriginRefused(t, mk(foreignOrigin, good).send(t, http.MethodPost, base+"/mcp", "application/json", []byte(`{}`)))

	routes := []struct{ method, path string }{
		{http.MethodGet, "/mcp/sse"},
		{http.MethodPost, "/mcp/message?sessionId=nope"},
		{http.MethodGet, "/mcp/nope"},
	}
	for _, rt := range routes {
		for _, who := range []struct{ name, auth string }{
			{"a valid key", good},
			{"a bad key", "ApiKey ghd_not_a_key"},
			{"no credentials", ""},
		} {
			t.Run(rt.method+" "+rt.path+" with a foreign Origin and "+who.name, func(t *testing.T) {
				require.Equal(t, want, refused(t, mk(foreignOrigin, who.auth), rt.method, rt.path))
			})
		}
		t.Run(rt.method+" "+rt.path+" with a repeated or null Origin", func(t *testing.T) {
			c := mk("", good)
			withOrigin(c, baseOrigin, baseOrigin)
			refused(t, c, rt.method, rt.path)
			refused(t, mk("null", good), rt.method, rt.path)
		})
	}

	// openSSE opens the stream with this Origin (none when "") and returns the
	// message endpoint the server announces, with the stream closed on cleanup.
	openSSE := func(t *testing.T, origin string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp/sse", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", good)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
		br := bufio.NewReader(resp.Body)
		for i := 0; i < 10; i++ {
			line, err := br.ReadString('\n')
			require.NoError(t, err)
			if strings.HasPrefix(line, "data: ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
		t.Fatal("no endpoint event")
		return ""
	}

	for _, origin := range []string{"", baseOrigin} {
		name := "no Origin"
		if origin != "" {
			name = "the right Origin"
		}
		t.Run("with "+name+" the stream opens and a message posts", func(t *testing.T) {
			endpoint := openSSE(t, origin)
			c := mk(origin, good)
			resp := c.send(t, http.MethodPost, base+endpoint, "application/json",
				[]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			body := readBody(t, resp)
			require.Equal(t, http.StatusAccepted, resp.StatusCode, body)
		})
	}

	t.Run("with no Origin, an unknown path is the transport's 404", func(t *testing.T) {
		resp := mk("", good).send(t, http.MethodGet, base+"/mcp/nope", "", nil)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("an anonymous caller without an Origin is still 401", func(t *testing.T) {
		for _, rt := range routes[:2] {
			resp := mk("", "").send(t, rt.method, base+rt.path, "application/json", []byte(`{}`))
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, rt.path)
		}
	})
}

// The case that makes the Origin check on /mcp/message matter. On /mcp a
// non-JSON POST is refused before anything runs, and a JSON POST needs a CORS
// preflight that is never granted. /mcp/message has neither barrier: it takes
// a text/plain body (a "simple" request, so no preflight) and runs it. A page
// on a sibling origin of the same site (here sibling.example.test, against a
// different BASE_URL) is same-site, so a SameSite=Lax session cookie IS sent
// with it, and the Origin check is the only thing standing between that page
// and the signed-in user's tools. docs/DESIGN.md, "MCP Interface" -> Origin.
func TestMCPOrigin_SSEMessageCheckedWhereNothingElseStopsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)

	s := &session{h: h}
	res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email": "staff@test.local", "password": "password",
	})
	require.Equal(t, http.StatusOK, res.StatusCode)

	// The stream is opened by the user's own tab: session cookie, no Origin.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp/sse", nil)
	require.NoError(t, err)
	for _, c := range s.jar {
		req.AddCookie(c)
	}
	stream, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Body.Close() })
	require.Equal(t, http.StatusOK, stream.StatusCode)
	events := bufio.NewReader(stream.Body)
	var endpoint string
	for i := 0; i < 10 && endpoint == ""; i++ {
		line, err := events.ReadString('\n')
		require.NoError(t, err)
		if strings.HasPrefix(line, "data: ") {
			endpoint = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	require.Contains(t, endpoint, "sessionId=")

	// waitFor reads the stream until a data line contains want.
	waitFor := func(t *testing.T, want string) {
		t.Helper()
		for {
			line, err := events.ReadString('\n')
			require.NoError(t, err, "no event containing %s", want)
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, want) {
				return
			}
		}
	}

	// post sends a text/plain body (what a cross-origin page can send without
	// a preflight) to the message endpoint with the user's session cookie.
	post := func(t *testing.T, origin string, payload map[string]any) *http.Response {
		t.Helper()
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "text/plain")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		for _, c := range s.jar {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	createTicket := func(id int, subject string) map[string]any {
		return map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "create_ticket", "arguments": map[string]any{
				"subject": subject, "category_id": h.catID.String(),
			}},
		}
	}

	before := latestTicketNumber(t, h)

	t.Run("control: a text/plain POST with the session cookie and no Origin runs", func(t *testing.T) {
		resp := post(t, "", createTicket(101, "control, no Origin"))
		require.Equal(t, http.StatusAccepted, resp.StatusCode, readBody(t, resp))
		waitFor(t, `"id":101`)
		require.Equal(t, before+2, latestTicketNumber(t, h), "the control took a number, and so did the probe")
	})

	t.Run("a text/plain POST with a valid session cookie and a same-site foreign Origin is refused and runs nothing", func(t *testing.T) {
		mid := latestTicketNumber(t, h)
		requireOriginRefused(t, post(t, "https://sibling.example.test", createTicket(102, "from a sibling page")))

		// Anything the refused POST had started would be on the stream ahead
		// of this ping's reply, or would already hold a ticket number.
		resp := post(t, "", map[string]any{"jsonrpc": "2.0", "id": 103, "method": "ping"})
		require.Equal(t, http.StatusAccepted, resp.StatusCode, readBody(t, resp))
		waitFor(t, `"id":103`)
		require.Equal(t, mid+1, latestTicketNumber(t, h), "only the probe may have taken a ticket number")
	})
}

// A Config with no BASE_URL has no origin to compare with. Startup refuses an
// empty one, but not every unusable one (TestOriginPolicy_Allows has those), so
// this pins the choice: a request that carries an Origin is refused, and one
// that does not goes on to authentication as usual.
func TestMCPOrigin_NoBaseURLFailsClosed(t *testing.T) {
	h, cleanup := newBareHarness(t)
	defer cleanup()

	reached := false
	handler := h.srv.ProtectMCP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	serve := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, origin := range []string{baseOrigin, foreignOrigin, "null"} {
		rec := serve(origin)
		requireOriginRefused(t, rec.Result())
	}
	require.False(t, reached)

	rec := serve("")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "no Origin: on to authentication")
}
