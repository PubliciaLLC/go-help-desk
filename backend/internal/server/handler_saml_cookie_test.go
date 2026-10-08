package server_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/stretchr/testify/require"
)

// The SAML library keeps its own login cookie, "token": a signed JWT, good for
// an hour, that /auth/saml/complete accepts in place of a fresh assertion.
// Revoking an app session (password change, MFA reset, a new factor) never
// reached it, so a browser still holding the cookie could walk back in, with
// whatever MFA the original assertion claimed (#337).
//
// The fix is to treat the cookie as a one-shot hand-over: /complete spends it
// the moment it has read it. The browser is told to delete it (same name,
// domain and path the library set it with), and its hash is recorded in a
// server-side ledger so a copy is refused. These tests drive the real handler
// with a cookie signed by the same key the live middleware verifies with.

// samlIdP serves just enough IdP metadata for samlsp.New to build a
// middleware. No assertion ever travels through it.
func samlIdP(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		md := saml.EntityDescriptor{
			EntityID: srv.URL + "/idp",
			IDPSSODescriptors: []saml.IDPSSODescriptor{{
				SingleSignOnServices: []saml.Endpoint{{
					Binding:  saml.HTTPRedirectBinding,
					Location: srv.URL + "/sso",
				}},
			}},
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		require.NoError(t, xml.NewEncoder(w).Encode(md))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// samlHarness is a harness with SAML live, plus a way to mint the library's
// cookie for a given person, signed as the real middleware would.
type samlHarness struct {
	*harness
	codec samlsp.JWTSessionCodec
	// issued is the (name, domain, path) the library gives its cookie when it
	// sets it. A browser deletes a cookie only when the deletion names the same
	// triple, so "cleared" below is judged against this, not against a name.
	issued cookieKey
	minted int
}

type cookieKey struct{ name, domain, path string }

func newSAMLHarness(t *testing.T) (*samlHarness, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
	idp := samlIdP(t)
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)

	block, _ := pem.Decode([]byte(validSAMLKey))
	require.NotNil(t, block)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)

	// Same root the production middleware is built with (see NewSAMLMiddleware):
	// it is the cookie's issuer and audience.
	sp, err := url.Parse(h.cfgBaseURL() + "/api/v1/auth/")
	require.NoError(t, err)
	opts := samlsp.Options{URL: *sp, Key: key}
	codec := samlsp.DefaultSessionCodec(opts)

	// Ask the library itself what it emits on login, rather than restating it.
	rec := httptest.NewRecorder()
	provider := samlsp.DefaultSessionProvider(opts)
	require.NoError(t, provider.CreateSession(rec, httptest.NewRequest(http.MethodPost, "/", nil),
		&saml.Assertion{Subject: &saml.Subject{NameID: &saml.NameID{Value: "x"}}}))
	set := rec.Result().Cookies()
	require.Len(t, set, 1)
	issued := cookieKey{name: set[0].Name, domain: set[0].Domain, path: set[0].Path}
	require.Equal(t, "token", issued.name)
	require.NotEmpty(t, issued.domain, "precondition: the library scopes its cookie to a domain")
	require.Equal(t, "/", issued.path)

	return &samlHarness{harness: h, codec: codec, issued: issued}, cleanup
}

func (h *harness) cfgBaseURL() string { return "http://localhost:8080" }

// libraryCookie is the "token" cookie the library would have set after a
// genuine assertion for this address.
func (sh *samlHarness) libraryCookie(t *testing.T, email string) *http.Cookie {
	t.Helper()
	attr := func(name, value string) saml.Attribute {
		return saml.Attribute{Name: name, Values: []saml.AttributeValue{{Value: value}}}
	}
	// A distinct SessionIndex, as a real IdP gives each authentication: the
	// spend is keyed on header.payload, so two cookies minted for one person in
	// the same second would otherwise be the same cookie.
	sh.minted++
	sess, err := sh.codec.New(&saml.Assertion{
		Subject:         &saml.Subject{NameID: &saml.NameID{Value: "sso-" + email}},
		AuthnStatements: []saml.AuthnStatement{{SessionIndex: fmt.Sprintf("idx-%d", sh.minted)}},
		AttributeStatements: []saml.AttributeStatement{{
			Attributes: []saml.Attribute{attr("email", email), attr("displayName", "SSO Person")},
		}},
	})
	require.NoError(t, err)
	val, err := sh.codec.Encode(sess)
	require.NoError(t, err)
	return &http.Cookie{Name: "token", Value: val}
}

// cleared reports whether res tells the browser to drop the library's cookie.
//
// Name, domain and path all have to match what the library set: a deletion
// with a different path or domain is a different cookie to the browser and
// leaves the real one in place, which looks like success in the response.
func (sh *samlHarness) cleared(res *http.Response) bool {
	for _, c := range res.Cookies() {
		if c.Name == sh.issued.name && c.Value == "" &&
			(c.MaxAge < 0 || c.Expires.Before(time.Now())) &&
			c.Domain == sh.issued.domain && c.Path == sh.issued.path {
			return true
		}
	}
	return false
}

// appSessionCookie returns the non-"token" cookie with a non-empty value, or nil.
func (sh *samlHarness) appSessionCookie(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name != "token" && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestSAMLComplete_SpendsTheLibraryCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	res := sh.rawGet(t, "/api/v1/auth/saml/complete",
		[]*http.Cookie{sh.libraryCookie(t, "sso@test.local")})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode, "precondition: the hand-over works")
	require.Equal(t, "/", res.Header.Get("Location"))
	require.True(t, sh.cleared(res),
		"the library cookie must be cleared (same name, domain and path it was set with) once it has been exchanged; otherwise a browser holding it can mint app sessions after every revocation (#337)")

	// The app session it was exchanged for is real.
	var appCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name != "token" && c.Value != "" {
			appCookie = c
		}
	}
	require.NotNil(t, appCookie, "an app session cookie is issued")
	me := sh.doUnauthWithCookie(t, http.MethodGet, "/api/v1/me", appCookie)
	me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)
}

// A refused sign-in spends the cookie too: it is the cookie, not the outcome,
// that must not survive, and a disabled person is exactly who gets revoked.
func TestSAMLComplete_SpendsTheLibraryCookieEvenWhenRefused(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()
	cookie := sh.libraryCookie(t, "sso@test.local")

	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{cookie})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)

	u, err := sh.userSvc.GetByEmail(context.Background(), "sso@test.local")
	require.NoError(t, err)
	r := sh.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+u.ID.String(), map[string]any{"disabled": true})
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)

	// Fresh cookie for the second request: the first was spent, so retesting the
	// same cookie tests the ledger, not the disability.
	cookie2 := sh.libraryCookie(t, "sso@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{cookie2})
	res.Body.Close()
	require.Equal(t, "/login?error=account_disabled", res.Header.Get("Location"))
	require.True(t, sh.cleared(res))
}

func TestSAMLComplete_RefusesASpentLibraryCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	c := sh.libraryCookie(t, "sso@test.local")

	// First /complete with c returns 303 to "/", with an app cookie.
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"))
	appCookie := sh.appSessionCookie(res)
	require.NotNil(t, appCookie, "an app session cookie is issued on first use")

	// Second /complete with the same c returns 303 with sso_session_used,
	// sets no app cookie, and clears the library cookie.
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"))
	require.Nil(t, sh.appSessionCookie(res), "no app cookie on replay")
	require.True(t, sh.cleared(res), "library cookie is cleared even on a spent cookie")
}

func TestSAMLComplete_CapturedCookieDoesNotSurviveRevocation(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	c := sh.libraryCookie(t, "sso@test.local")

	// c signs in and returns app cookie A. /me with A returns 200.
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	appCookie := sh.appSessionCookie(res)
	require.NotNil(t, appCookie)

	me := sh.doUnauthWithCookie(t, http.MethodGet, "/api/v1/me", appCookie)
	me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)

	// Look up the user, then doAsAdmin PATCH /api/v1/admin/users/{id} to
	// reset_mfa. This revokes existing sessions.
	u, err := sh.userSvc.GetByEmail(context.Background(), "sso@test.local")
	require.NoError(t, err)
	r := sh.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+u.ID.String(), map[string]any{"reset_mfa": true})
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)

	// /me with A returns 401: the revocation happened.
	me = sh.doUnauthWithCookie(t, http.MethodGet, "/api/v1/me", appCookie)
	me.Body.Close()
	require.Equal(t, http.StatusUnauthorized, me.StatusCode)

	// /complete with c redirects to sso_session_used, and no app cookie is issued.
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"))
	require.Nil(t, sh.appSessionCookie(res))
}

func TestSAMLComplete_RefusedSignInStillSpendsTheCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	// Create the account: /complete with a fresh cookie returns "/".
	c1 := sh.libraryCookie(t, "sso@test.local")
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c1})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"))

	// Disable it through doAsAdmin PATCH.
	u, err := sh.userSvc.GetByEmail(context.Background(), "sso@test.local")
	require.NoError(t, err)
	r := sh.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+u.ID.String(), map[string]any{"disabled": true})
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)

	// c2 is fresh. /complete with c2 returns account_disabled.
	c2 := sh.libraryCookie(t, "sso@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c2})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=account_disabled", res.Header.Get("Location"))

	// Re-enable the account.
	r = sh.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+u.ID.String(), map[string]any{"disabled": false})
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)

	// /complete with c2 again returns sso_session_used, not account_disabled:
	// c2 was already spent on the refused sign-in.
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c2})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"))
}

// TestSAMLComplete_RefusesARespelledSpentCookie: the spend is keyed on the
// signed input (header.payload), not on the cookie text. The JWT verifier
// decodes the signature leniently, so one token has several spellings that all
// verify; keyed on the whole value, each would hash differently and a captured
// cookie could be replayed by respelling its signature (#337).
//
// Only one respelling is accepted by the library in use: the unused low bits of
// the signature's last base64 character (an ES256 signature is 64 bytes, so the
// 86th character carries 2 data bits and 4 unused ones). '=' padding, alone or
// combined with that, is rejected by the verifier (golang-jwt v4 leaves
// DecodePaddingAllowed off), so it is not a replay route and is not tested as one.
func TestSAMLComplete_RefusesARespelledSpentCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	// respell flips a bit that does not belong to the signature: same bytes,
	// different text.
	respell := func(v string) string {
		last := strings.IndexByte(alphabet, v[len(v)-1])
		require.GreaterOrEqual(t, last, 0)
		return v[:len(v)-1] + string(alphabet[last^1])
	}

	// Precondition: the library accepts the respelling, so the replay below
	// proves something. A fresh, unspent cookie respelled must sign in.
	fresh := sh.libraryCookie(t, "sso-fresh@test.local")
	require.NotEqual(t, fresh.Value, respell(fresh.Value))
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: respell(fresh.Value)}})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"), "precondition: the respelled signature verifies")

	// Padding is not accepted by the library (documented above); assert it so
	// this comment cannot go stale if the library changes.
	fresh = sh.libraryCookie(t, "sso-padded@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: fresh.Value + "=="}})
	res.Body.Close()
	require.NotEqual(t, "/", res.Header.Get("Location"), "padded signature must not verify")

	// The attack: spend the cookie, then replay it respelled.
	c := sh.libraryCookie(t, "sso@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"))

	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: respell(c.Value)}})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"))
	require.Nil(t, sh.appSessionCookie(res))
	require.True(t, sh.cleared(res))
}
