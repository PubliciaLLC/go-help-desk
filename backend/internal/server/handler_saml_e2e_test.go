package server_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"
)

// fakeIdP is the library's own saml.IdentityProvider, signing with the
// harness's throwaway EC pair. Its metadata (with the signing cert) is what
// the SP is configured from, so the SP verifies the assertion for real.
type fakeIdP struct {
	idp *saml.IdentityProvider
	srv *httptest.Server
}

// spOnlyACS answers the IdP's "who is this SP" lookup with an ACS endpoint and
// no KeyDescriptor, so the IdP does not try to encrypt the assertion to the
// SP's EC certificate (xmlenc's key transport is RSA only).
type spOnlyACS struct{ acs string }

func (s spOnlyACS) GetServiceProvider(_ *http.Request, id string) (*saml.EntityDescriptor, error) {
	return &saml.EntityDescriptor{
		EntityID: id,
		SPSSODescriptors: []saml.SPSSODescriptor{{
			AssertionConsumerServices: []saml.IndexedEndpoint{{
				Binding: saml.HTTPPostBinding, Location: s.acs, Index: 1,
			}},
		}},
	}, nil
}

func newFakeIdP(t *testing.T, acs string) *fakeIdP {
	t.Helper()
	block, _ := pem.Decode([]byte(validSAMLKey))
	key, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)
	cblock, _ := pem.Decode([]byte(validSAMLCert))
	cert, err := x509.ParseCertificate(cblock.Bytes)
	require.NoError(t, err)

	f := &fakeIdP{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.idp.ServeMetadata(w, r)
	}))
	t.Cleanup(f.srv.Close)
	base, _ := url.Parse(f.srv.URL)
	f.idp = &saml.IdentityProvider{
		Key:                     key,
		Signer:                  key,
		Certificate:             cert,
		MetadataURL:             *base.ResolveReference(&url.URL{Path: "/metadata"}),
		SSOURL:                  *base.ResolveReference(&url.URL{Path: "/sso"}),
		ServiceProviderProvider: spOnlyACS{acs: acs},
		SignatureMethod:         "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256", // the library defaults to rsa-sha1; the harness key is EC
	}
	return f
}

// sign plays the IdP's half: it takes the SAMLRequest redirect our login route
// produced and returns the auto-POST form (SAMLResponse, RelayState) a real IdP
// would send the browser back with.
func (f *fakeIdP) sign(t *testing.T, redirect string, email string) url.Values {
	t.Helper()
	req, err := saml.NewIdpAuthnRequest(f.idp, httptest.NewRequest(http.MethodGet, redirect, nil))
	require.NoError(t, err)
	require.NoError(t, req.Validate())
	require.NoError(t, saml.DefaultAssertionMaker{}.MakeAssertion(req, &saml.Session{
		ID: "s1", NameID: "sso-" + email, UserEmail: email, UserCommonName: "SSO Person",
	}))
	form, err := req.PostBinding()
	require.NoError(t, err)
	return url.Values{"SAMLResponse": {form.SAMLResponse}, "RelayState": {form.RelayState}}
}

func TestSAMLSignIn_EndToEndWithASignedAssertion(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.srv.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)

	// 1. Login: redirect to the IdP, tracking cookie set.
	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	var tracking *http.Cookie
	for _, c := range res.Cookies() {
		if strings.HasPrefix(c.Name, "saml_") {
			tracking = c
		}
	}
	require.NotNil(t, tracking)

	// 2. IdP signs; browser POSTs to the ACS carrying the tracking cookie.
	form := idp.sign(t, res.Header.Get("Location"), "sso@test.local")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(tracking)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	res = rr.Result()
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode, "ACS body: %s", rr.Body.String())
	require.Equal(t, "/api/v1/auth/saml/complete", res.Header.Get("Location"))
	var token *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "token" && c.Value != "" {
			token = c
		}
	}
	require.NotNil(t, token)

	// 3. /complete spends the hand-over and writes the app session.
	res = h.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{token})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"))
	var app *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name != "token" && c.Value != "" {
			app = c
		}
	}
	require.NotNil(t, app, "an app session cookie is issued")
	me := h.doUnauthWithCookie(t, http.MethodGet, "/api/v1/me", app)
	me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)

	// 4. Replay refused.
	res = h.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{token})
	res.Body.Close()
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"))
}

// Without the tracking cookie the ACS must refuse the assertion.
func TestSAMLSignIn_ACSRefusesWithoutTrackingCookie(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.srv.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)

	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	form := idp.sign(t, res.Header.Get("Location"), "sso@test.local")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	res = rr.Result()
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	for _, c := range res.Cookies() {
		require.NotEqual(t, "token", c.Name)
	}
}
