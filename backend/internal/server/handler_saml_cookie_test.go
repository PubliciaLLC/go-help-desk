package server_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// the moment it has read it. These tests drive the real handler with a cookie
// signed by the same key the live middleware verifies with.

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
}

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
	codec := samlsp.DefaultSessionCodec(samlsp.Options{URL: *sp, Key: key})
	return &samlHarness{harness: h, codec: codec}, cleanup
}

func (h *harness) cfgBaseURL() string { return "http://localhost:8080" }

// libraryCookie is the "token" cookie the library would have set after a
// genuine assertion for this address.
func (sh *samlHarness) libraryCookie(t *testing.T, email string) *http.Cookie {
	t.Helper()
	attr := func(name, value string) saml.Attribute {
		return saml.Attribute{Name: name, Values: []saml.AttributeValue{{Value: value}}}
	}
	sess, err := sh.codec.New(&saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "sso-" + email}},
		AttributeStatements: []saml.AttributeStatement{{
			Attributes: []saml.Attribute{attr("email", email), attr("displayName", "SSO Person")},
		}},
	})
	require.NoError(t, err)
	val, err := sh.codec.Encode(sess)
	require.NoError(t, err)
	return &http.Cookie{Name: "token", Value: val}
}

// cleared reports whether res tells the browser to drop the named cookie.
func cleared(res *http.Response, name string) bool {
	for _, c := range res.Cookies() {
		if c.Name == name && c.Value == "" && (c.MaxAge < 0 || c.Expires.Before(time.Now())) {
			return true
		}
	}
	return false
}

func TestSAMLComplete_SpendsTheLibraryCookie(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	res := sh.rawGet(t, "/api/v1/auth/saml/complete",
		[]*http.Cookie{sh.libraryCookie(t, "sso@test.local")})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode, "precondition: the hand-over works")
	require.Equal(t, "/", res.Header.Get("Location"))
	require.True(t, cleared(res, "token"),
		"the library cookie must be cleared once it has been exchanged; otherwise a browser holding it can mint app sessions after every revocation (#337)")

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

	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{cookie})
	res.Body.Close()
	require.Equal(t, "/login?error=account_disabled", res.Header.Get("Location"))
	require.True(t, cleared(res, "token"))
}
