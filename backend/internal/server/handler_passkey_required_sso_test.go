package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #616 part c, SSO. A role that must use a passkey is let in by SAML or OIDC
// only when the provider asserted a passkey-grade sign-in (values documented
// for Entra, auth/ssofactor.go). Otherwise there is a passkey step after the
// SSO, which the browser is sent to as /login?sso_step=<code>.

const (
	ssoMethodsAttr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
	ssoMultiple    = "http://schemas.microsoft.com/claims/multipleauthn"
)

func meStatus(t *testing.T, h *harness, cookies []*http.Cookie) int {
	t.Helper()
	res := h.rawGet(t, "/api/v1/me", cookies)
	res.Body.Close()
	return res.StatusCode
}

func sessionFrom(t *testing.T, h *harness, cookies []*http.Cookie) auth.SessionData {
	t.Helper()
	_, sd, ok := storedSession(t, &session{h: h, jar: cookies})
	require.True(t, ok)
	return sd
}

// ── OIDC ─────────────────────────────────────────────────────────────────────

func TestOIDCCallback_PasskeyRequired(t *testing.T) {
	claims := fakeOIDCClaims{Subject: "pk-sub", Email: "pk@test.local", EmailVerified: true, Name: "Pk"}

	cases := []struct {
		name         string
		listed       bool
		amr          []string
		prepare      func(t *testing.T, oh *oidcHarness, u user.User)
		wantLocation string
		wantMe       int
		wantOwed     bool
	}{
		{"no passkey-grade claim, no factor: register a key", true, nil, nil,
			"/login?sso_step=passkey_enroll", http.StatusForbidden, true},
		{"mfa alone is not a passkey", true, []string{"pwd", "mfa"}, nil,
			"/login?sso_step=passkey_enroll", http.StatusForbidden, true},
		{"amr fido", true, []string{"fido"}, nil, "/", http.StatusOK, false},
		{"amr hwk with mfa", true, []string{"hwk", "mfa"}, nil, "/", http.StatusOK, false},
		{"a registered key is used", true, nil,
			func(t *testing.T, oh *oidcHarness, u user.User) { addKey(t, oh.harness, u.ID) },
			"/login?sso_step=passkey", http.StatusForbidden, true},
		{"an authenticator is verified first", true, nil,
			func(t *testing.T, oh *oidcHarness, u user.User) { enrollMFA(t, context.Background(), oh.userSvc, u.ID) },
			"/login?sso_step=verify_then_passkey_enroll", http.StatusForbidden, true},
		{"an authenticator, provider already asserted MFA", true, []string{"mfa"},
			func(t *testing.T, oh *oidcHarness, u user.User) { enrollMFA(t, context.Background(), oh.userSvc, u.ID) },
			"/login?sso_step=passkey_enroll", http.StatusForbidden, true},
		{"a role not listed goes straight in", false, nil, nil, "/", http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oh, cleanup := newOIDCHarness(t)
			defer cleanup()
			require.NoError(t, oh.adminSvc.SetBool(context.Background(), "mfa_enabled", true))

			// First sign-in provisions the account (role user); not listed yet.
			first := oh.login(t, claims)
			require.Equal(t, "/", first.Header.Get("Location"))
			u, ok := findUser(t, oh.harness, claims.Email)
			require.True(t, ok)
			if tc.prepare != nil {
				tc.prepare(t, oh, u)
			}
			if tc.listed {
				requirePasskeysFor(t, oh.harness, "user")
			} else {
				requirePasskeysFor(t, oh.harness, "admin")
			}

			c := claims
			c.AMR = tc.amr
			resp := oh.login(t, c)
			require.Equal(t, http.StatusSeeOther, resp.StatusCode)
			require.Equal(t, tc.wantLocation, resp.Header.Get("Location"))
			require.Equal(t, tc.wantMe, meStatus(t, oh.harness, resp.Cookies()))
			require.Equal(t, tc.wantOwed, sessionFrom(t, oh.harness, resp.Cookies()).PasskeyOwed)
		})
	}
}

// Suspended while passkeys are unavailable: in as today.
func TestOIDCCallback_PasskeyRequired_SuspendedWhilePasskeysUnavailable(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	oh := newOIDCHarnessOn(t, h)
	requirePasskeysFor(t, h, "user")

	resp := oh.login(t, fakeOIDCClaims{Subject: "off-sub", Email: "off@test.local", EmailVerified: true, Name: "Off"})
	require.Equal(t, "/", resp.Header.Get("Location"))
	require.Equal(t, http.StatusOK, meStatus(t, h, resp.Cookies()))
}

func newOIDCHarnessOn(t *testing.T, h *harness) *oidcHarness {
	t.Helper()
	idp := newFakeOIDC(t)
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetOIDCConfig(ctx, auth.OIDCConfig{
		Enabled:      true,
		IssuerURL:    idp.issuer(),
		ClientID:     idp.clientID,
		ClientSecret: idp.clientSecret,
		RedirectURL:  "http://app.test/api/v1/auth/oidc/callback",
	}))
	require.NoError(t, h.srv.InitOIDC(ctx))
	return &oidcHarness{harness: h, idp: idp}
}

// ── SAML ─────────────────────────────────────────────────────────────────────

func samlAttrs(values ...string) []saml.Attribute {
	a := saml.Attribute{Name: ssoMethodsAttr}
	for _, v := range values {
		a.Values = append(a.Values, saml.AttributeValue{Type: "xs:string", Value: v})
	}
	return []saml.Attribute{a}
}

// samlComplete runs a whole SAML sign-in for email with the given attributes
// and returns the response of the final hand-over (/saml/complete).
func samlComplete(t *testing.T, h *harness, idp *fakeIdP, email string, attrs []saml.Attribute) *http.Response {
	t.Helper()
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

	form := idp.signWithAttrs(t, res.Header.Get("Location"), email, attrs)
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
	return res
}

func samlIdPFor(t *testing.T, h *harness) *fakeIdP {
	t.Helper()
	ctx := context.Background()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.srv.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)
	return idp
}

func appCookies(res *http.Response) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range res.Cookies() {
		if c.Name != "token" && c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

func TestSAMLComplete_PasskeyRequired(t *testing.T) {
	cases := []struct {
		name         string
		listed       bool
		attrs        []saml.Attribute
		wantLocation string
		wantMe       int
	}{
		{"no assertion", true, nil, "/login?sso_step=passkey_enroll", http.StatusForbidden},
		{"MFA asserted, not a passkey", true, samlAttrs(ssoMultiple), "/login?sso_step=passkey_enroll", http.StatusForbidden},
		{"fido alone", true, samlAttrs("fido"), "/login?sso_step=passkey_enroll", http.StatusForbidden},
		{"entra fido with multipleauthn", true, samlAttrs("fido", ssoMultiple), "/", http.StatusOK},
		{"entra hwk with multipleauthn", true, samlAttrs(ssoMultiple, "hwk"), "/", http.StatusOK},
		{"a role not listed goes straight in", false, nil, "/", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			idp := samlIdPFor(t, h)
			if tc.listed {
				requirePasskeysFor(t, h, "user")
			} else {
				requirePasskeysFor(t, h, "admin")
			}

			res := samlComplete(t, h, idp, "sso@test.local", tc.attrs)
			require.Equal(t, http.StatusSeeOther, res.StatusCode)
			require.Equal(t, tc.wantLocation, res.Header.Get("Location"))
			cookies := appCookies(res)
			require.NotEmpty(t, cookies)
			require.Equal(t, tc.wantMe, meStatus(t, h, cookies))
			sd := sessionFrom(t, h, cookies)
			require.Equal(t, tc.wantMe != http.StatusOK, sd.PasskeyOwed)
			if tc.listed && tc.wantMe == http.StatusOK {
				require.True(t, sd.FactorVerified, "a passkey-grade assertion proves a factor")
			}
		})
	}
}

func TestSAMLComplete_PasskeyRequired_SuspendedWhilePasskeysUnavailable(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	idp := samlIdPFor(t, h)
	requirePasskeysFor(t, h, "user")

	res := samlComplete(t, h, idp, "sso@test.local", nil)
	require.Equal(t, "/", res.Header.Get("Location"))
	require.Equal(t, http.StatusOK, meStatus(t, h, appCookies(res)))
}

// A pending SSO session can register a key and then is in. The first sign-in
// has no factor, so registering is allowed from a session that proved none.
func TestSAMLComplete_PasskeyRequired_RegisteringAKeyFinishesTheSignIn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	idp := samlIdPFor(t, h)
	requirePasskeysFor(t, h, "user")

	res := samlComplete(t, h, idp, "sso@test.local", nil)
	require.Equal(t, "/login?sso_step=passkey_enroll", res.Header.Get("Location"))
	s := &session{h: h, jar: appCookies(res)}

	reg, body := registerKey(t, s)
	require.Equal(t, http.StatusNoContent, reg.StatusCode, "body: %s", body)
	keepLastCookies(s)
	requireSignedIn(t, s)
}
