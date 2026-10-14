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
//   - on /mcp and /mcp/ only. /mcp/sse and /mcp/message are not checked (see
//     TestMCPOrigin_SSEIsNotChecked);
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

// The decision, pinned: the SSE transport (/mcp/sse, /mcp/message) is NOT
// subject to the Origin check. docs/DESIGN.md says why. If this test is
// changed to expect 403, that is a decision to apply the check to SSE, not a
// fix.
func TestMCPOrigin_SSEIsNotChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	base := serveMCP(t, h)

	mk := func() *streamConn {
		c := withOrigin(&streamConn{base: base, header: http.Header{}}, foreignOrigin)
		c.header.Set("Authorization", "ApiKey "+h.apiKey)
		return c
	}

	t.Run("the stream opens", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp/sse", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "ApiKey "+h.apiKey)
		req.Header.Set("Origin", foreignOrigin)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(line, "event:"), line)
	})

	t.Run("a message is not refused for its Origin", func(t *testing.T) {
		// No such session, so the transport itself says no, and says it in its
		// own words rather than ours.
		resp := mk().send(t, http.MethodPost, base+"/mcp/message?sessionId=nope", "application/json", []byte(`{}`))
		body := readBody(t, resp)
		require.NotContains(t, body, "origin_not_allowed")
		require.NotEqual(t, http.StatusForbidden, resp.StatusCode, body)
	})

	t.Run("an unknown path under /mcp/ is the transport's 404", func(t *testing.T) {
		resp := mk().send(t, http.MethodGet, base+"/mcp/nope", "", nil)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("an anonymous caller is still 401", func(t *testing.T) {
		c := withOrigin(&streamConn{base: base, header: http.Header{}}, foreignOrigin)
		resp := c.send(t, http.MethodGet, base+"/mcp/sse", "", nil)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
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
