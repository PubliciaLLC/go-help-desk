package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"
)

// signWithAttrs is fakeIdP.sign with attributes in the assertion, which is how
// an identity provider says it ran MFA.
func (f *fakeIdP) signWithAttrs(t *testing.T, redirect, email string, attrs []saml.Attribute) url.Values {
	t.Helper()
	req, err := saml.NewIdpAuthnRequest(f.idp, httptest.NewRequest(http.MethodGet, redirect, nil))
	require.NoError(t, err)
	require.NoError(t, req.Validate())
	require.NoError(t, saml.DefaultAssertionMaker{}.MakeAssertion(req, &saml.Session{
		ID: "s1", NameID: "sso-" + email, UserEmail: email, UserCommonName: "SSO Person",
		CustomAttributes: attrs,
	}))
	form, err := req.PostBinding()
	require.NoError(t, err)
	return url.Values{"SAMLResponse": {form.SAMLResponse}, "RelayState": {form.RelayState}}
}

// FactorVerified is what lets a session add or replace a second factor (#333),
// so it must be true after a SAML sign-in only when the assertion said MFA ran.
// samlAssertedMFA has a unit test; this is the one that reads the session the
// browser is actually left holding.
func TestSAMLSignIn_FactorVerifiedFollowsTheAssertion(t *testing.T) {
	const (
		methodsAttr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
		mfaValue    = "http://schemas.microsoft.com/claims/multipleauthn"
		pwdValue    = "http://schemas.microsoft.com/ws/2008/06/identity/authenticationmethod/password"
	)
	attr := func(values ...string) []saml.Attribute {
		a := saml.Attribute{Name: methodsAttr}
		for _, v := range values {
			a.Values = append(a.Values, saml.AttributeValue{Type: "xs:string", Value: v})
		}
		return []saml.Attribute{a}
	}

	for _, tc := range []struct {
		name  string
		attrs []saml.Attribute
		want  bool
	}{
		{"assertion without MFA", nil, false},
		{"assertion naming only the password", attr(pwdValue), false},
		{"assertion that says MFA ran", attr(pwdValue, mfaValue), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
			require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.srv.URL, validSAMLCert, validSAMLKey))
			h.srv.InitSAML(ctx)

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

			form := idp.signWithAttrs(t, res.Header.Get("Location"), "sso@test.local", tc.attrs)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(tracking)
			rr := httptest.NewRecorder()
			h.srv.ServeHTTP(rr, req)
			res = rr.Result()
			res.Body.Close()
			require.Equal(t, http.StatusFound, res.StatusCode, "ACS body: %s", rr.Body.String())
			var token *http.Cookie
			for _, c := range res.Cookies() {
				if c.Name == "token" && c.Value != "" {
					token = c
				}
			}
			require.NotNil(t, token)

			res = h.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{token})
			res.Body.Close()
			require.Equal(t, http.StatusSeeOther, res.StatusCode)
			var app *http.Cookie
			for _, c := range res.Cookies() {
				if c.Name != "token" && c.Value != "" {
					app = c
				}
			}
			require.NotNil(t, app, "an app session cookie is issued")

			_, sd, ok := storedSession(t, &session{h: h, jar: []*http.Cookie{app}})
			require.True(t, ok)
			require.True(t, sd.MFAPassed, "SAML sign-in counts as MFA for access")
			require.Equal(t, tc.want, sd.FactorVerified)
		})
	}
}
