package server_test

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml/samlsp"
	"github.com/stretchr/testify/require"
)

// trackedRequest decodes the library's request-tracking cookie ("saml_" +
// RelayState) from a login response.
func (sh *samlHarness) trackedRequest(t *testing.T, res *http.Response) (*samlsp.TrackedRequest, *http.Cookie) {
	t.Helper()
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	relay := loc.Query().Get("RelayState")
	require.NotEmpty(t, relay, "the IdP redirect carries a RelayState")

	block, _ := pem.Decode([]byte(validSAMLKey))
	key, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)
	sp, err := url.Parse(sh.cfgBaseURL() + "/api/v1/auth/")
	require.NoError(t, err)
	codec := samlsp.DefaultTrackedRequestCodec(samlsp.Options{URL: *sp, Key: key})

	for _, c := range res.Cookies() {
		if c.Name == "saml_"+relay {
			tr, err := codec.Decode(c.Value)
			require.NoError(t, err)
			return tr, c
		}
	}
	t.Fatalf("no saml_%s cookie set", relay)
	return nil, nil
}

// GET /auth/saml/login used to hand the request to samlsp's ServeHTTP, which
// serves only the metadata and ACS paths and answered 404 (#390). It must
// start the IdP redirect, and the tracking cookie it sets must send the
// browser to /saml/complete after the ACS, as a bare path.
func TestSAMLLogin_RedirectsToTheIdP(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	res := sh.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/sso", loc.Path, "the redirect goes to the IdP's SSO endpoint")
	require.NotEmpty(t, loc.Query().Get("SAMLRequest"))

	tr, c := sh.trackedRequest(t, res)
	require.Equal(t, "/api/v1/auth/saml/complete", tr.URI,
		"after the ACS the browser must land on /complete, as a path: /login would loop, an absolute URL would take the Host header and drop https behind a proxy")
	require.Equal(t, "/api/v1/auth/saml/acs", c.Path, "the tracking cookie is scoped to the ACS")
	require.True(t, c.HttpOnly)
}

// The login route reads nothing from the query, so it cannot be turned into
// a redirect to somewhere else.
func TestSAMLLogin_IgnoresReturnToParameters(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	for _, q := range []string{
		"?return_to=https://evil.example/",
		"?next=https://evil.example/",
		"?RelayState=https://evil.example/",
		"?redirect=//evil.example/",
	} {
		res := sh.rawGet(t, "/api/v1/auth/saml/login"+q, nil)
		res.Body.Close()
		require.Equal(t, http.StatusFound, res.StatusCode, q)
		tr, _ := sh.trackedRequest(t, res)
		require.Equal(t, "/api/v1/auth/saml/complete", tr.URI, q)
		require.NotContains(t, res.Header.Get("Location"), "evil.example", q)
	}
}

// The stored post-ACS URI must not be derived from the Host or X-Forwarded-*
// headers: an attacker-chosen host would receive the browser after sign-in,
// and X-Forwarded-Proto=http would drop the Secure session cookie.
func TestSAMLLogin_IgnoresHostAndForwardedHeaders(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/login", nil)
	req.Host = "evil.example"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "http")
	rr := httptest.NewRecorder()
	sh.srv.ServeHTTP(rr, req)
	res := rr.Result()
	res.Body.Close()

	require.Equal(t, http.StatusFound, res.StatusCode)
	tr, _ := sh.trackedRequest(t, res)
	require.Equal(t, "/api/v1/auth/saml/complete", tr.URI)
}

// A "token" cookie already in the browser must not answer for the IdP. Going
// through /complete would spend a fresh one without asking the IdP, and a
// spent one would end at sso_session_used with no way to start over.
func TestSAMLLogin_StartsFreshEvenWithAHandoverCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	spent := sh.libraryCookie(t, "sso@test.local")
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{spent})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"), "precondition: the cookie is now spent")

	for name, c := range map[string]*http.Cookie{
		"spent":  spent,
		"unused": sh.libraryCookie(t, "sso@test.local"),
	} {
		res := sh.rawGet(t, "/api/v1/auth/saml/login", []*http.Cookie{c})
		res.Body.Close()
		require.Equal(t, http.StatusFound, res.StatusCode, name)
		loc, err := url.Parse(res.Header.Get("Location"))
		require.NoError(t, err, name)
		require.Equal(t, "/sso", loc.Path, name)
		// Only the tracking cookie: no app session, and the hand-over cookie
		// is neither spent nor touched here; /complete does that.
		for _, set := range res.Cookies() {
			require.True(t, strings.HasPrefix(set.Name, "saml_"), "%s: login set %q", name, set.Name)
		}
	}
}

func TestSAMLLogin_NotConfigured(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}
