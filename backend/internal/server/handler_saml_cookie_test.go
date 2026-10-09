package server_test

import (
	"context"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
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
// domain and path the library set it with), and the SHA-256 of its signed input
// (header.payload, not the signature) is recorded in a server-side ledger so a
// copy is refused. These tests drive the real handler with a cookie signed by
// the same key the live middleware verifies with.

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
	codec samlsp.SessionCodec
	// issued is the (name, domain, path) the library gives its cookie when it
	// sets it. A browser deletes a cookie only when the deletion names the same
	// triple, so "cleared" below is judged against this, not against a name.
	issued cookieKey
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
	// Use HandoverCodec to match production behavior exactly, including random jti.
	codec := auth.NewHandoverCodec(opts)

	// Ask the library itself what it emits on login, rather than restating it.
	rec := httptest.NewRecorder()
	provider := samlsp.DefaultSessionProvider(opts)
	provider.MaxAge = auth.SAMLHandoverMaxAge
	provider.Codec = codec
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
// Two respellings are accepted by the library in use:
//  1. The unused low bits of the signature's last base64 character (an ES256
//     signature is 64 bytes, so the 86th character carries 2 data bits and
//     4 unused ones).
//  2. ECDSA high-S: for the 64-byte signature r||s, replacing s with N-s
//     (where N is the P-256 group order) still verifies.
//
// '=' padding is rejected by the verifier (golang-jwt v4 leaves
// DecodePaddingAllowed off), so it is not a replay route and is not tested.
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

	// respellHighS computes the high-S twin of a JWT signature.
	// For ES256, the signature is r||s (32 bytes each). High-S replaces s with
	// N - s, where N is the P-256 group order.
	respellHighS := func(jwtValue string) string {
		parts := strings.Split(jwtValue, ".")
		require.Len(t, parts, 3, "JWT must be header.payload.signature")

		// Decode the signature (last part).
		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		require.NoError(t, err, "signature must be valid base64url")
		require.Len(t, sigBytes, 64, "ES256 signature must be 64 bytes (r||s)")

		// Extract r and s (32 bytes each).
		r := new(big.Int).SetBytes(sigBytes[:32])
		s := new(big.Int).SetBytes(sigBytes[32:64])

		// Compute s' = N - s, where N is the P-256 group order.
		N := elliptic.P256().Params().N
		sPrime := new(big.Int).Sub(N, s)

		// Reconstruct the signature as r||s'.
		newSigBytes := make([]byte, 64)
		rBytes := r.Bytes()
		copy(newSigBytes[32-len(rBytes):32], rBytes)
		sPrimeBytes := sPrime.Bytes()
		copy(newSigBytes[64-len(sPrimeBytes):64], sPrimeBytes)

		// Re-encode and rebuild the JWT.
		newSig := base64.RawURLEncoding.EncodeToString(newSigBytes)
		return parts[0] + "." + parts[1] + "." + newSig
	}

	// Precondition: the library accepts the low-bit respelling, so the replay
	// below proves something. A fresh, unspent cookie respelled must sign in.
	fresh := sh.libraryCookie(t, "sso-fresh@test.local")
	require.NotEqual(t, fresh.Value, respell(fresh.Value))
	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: respell(fresh.Value)}})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"), "precondition: the low-bit respelled signature verifies")

	// Precondition: the library accepts high-S respelling too.
	fresh = sh.libraryCookie(t, "sso-fresh-hs@test.local")
	highS := respellHighS(fresh.Value)
	require.NotEqual(t, fresh.Value, highS)
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: highS}})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"), "precondition: the high-S respelled signature verifies")

	// Padding is not accepted by the library (documented above); assert it so
	// this comment cannot go stale if the library changes.
	fresh = sh.libraryCookie(t, "sso-padded@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: fresh.Value + "=="}})
	res.Body.Close()
	require.NotEqual(t, "/", res.Header.Get("Location"), "padded signature must not verify")

	// The attack: spend the cookie, then replay it respelled with low-bit flip.
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

	// The attack with high-S respelling: spend a fresh cookie, then replay it
	// respelled with high-S.
	c2 := sh.libraryCookie(t, "sso-hs@test.local")
	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c2})
	res.Body.Close()
	require.Equal(t, "/", res.Header.Get("Location"))

	res = sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{{Name: "token", Value: respellHighS(c2.Value)}})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/login?error=sso_session_used", res.Header.Get("Location"),
		"spent cookie replayed with high-S respelling must be refused")
	require.Nil(t, sh.appSessionCookie(res))
	require.True(t, sh.cleared(res))
}

// TestSAMLComplete_LedgerWriteFailureFailsClosed: if the single-use record
// cannot be written, the sign-in is refused. Letting it through would turn a
// database fault into a way to replay a cookie.
//
// The failure is injected at the store (FailSAMLSpendForTest), not by breaking
// the table: a SQL error aborts the harness's shared transaction, so a handler
// that wrongly carried on would fail on its next query and the test could not
// tell the difference.
func TestSAMLComplete_LedgerWriteFailureFailsClosed(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()
	sh.srv.FailSAMLSpendForTest()

	res := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{sh.libraryCookie(t, "sso@test.local")})
	res.Body.Close()

	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Nil(t, sh.appSessionCookie(res), "no app session when the ledger write fails")
}

// TestSAMLComplete_TwoMintsInTheSameSecondBothSignIn verifies that two SAML
// handover tokens minted in the same second for the same person can both be
// used to sign in. This tests the fix for #337: without random jti on each
// mint, the two tokens would have identical header.payload and the spend
// (keyed on header.payload) would reject the second one.
func TestSAMLComplete_TwoMintsInTheSameSecondBothSignIn(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	// Mint two cookies for the same email in rapid succession.
	c1 := sh.libraryCookie(t, "sso@test.local")
	c2 := sh.libraryCookie(t, "sso@test.local")

	// Both should sign in successfully (both return 303 to "/").
	res1 := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c1})
	res1.Body.Close()
	require.Equal(t, http.StatusSeeOther, res1.StatusCode)
	require.Equal(t, "/", res1.Header.Get("Location"))

	res2 := sh.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c2})
	res2.Body.Close()
	require.Equal(t, http.StatusSeeOther, res2.StatusCode)
	require.Equal(t, "/", res2.Header.Get("Location"))

	// Both should have issued an app session cookie.
	appCookie1 := sh.appSessionCookie(res1)
	require.NotNil(t, appCookie1, "first mint must sign in and issue an app cookie")

	appCookie2 := sh.appSessionCookie(res2)
	require.NotNil(t, appCookie2, "second mint must sign in and issue an app cookie")
}

func TestSAMLComplete_QuotedEmailIsRefusedNotAnInternalError(t *testing.T) {
	sh, cleanup := newSAMLHarness(t)
	defer cleanup()

	res := sh.rawGet(t, "/api/v1/auth/saml/complete",
		[]*http.Cookie{sh.libraryCookie(t, `"john doe"@test.local`)})
	res.Body.Close()

	// Should redirect to login with error, not return 500
	require.Equal(t, http.StatusSeeOther, res.StatusCode,
		"a quoted email must redirect, not return 500")
	require.Equal(t, "/login?error=invalid_email", res.Header.Get("Location"),
		"a quoted email must redirect to /login?error=invalid_email")

	// Verify no user was created
	users, err := sh.userSvc.ListAdmin(context.Background(), 500, 0)
	require.NoError(t, err)
	for _, u := range users {
		require.NotEqual(t, `"john doe"@test.local`, u.Email, "quoted email must not create a user")
		require.NotEqual(t, "john doe@test.local", u.Email, "normalized version must not create a user")
	}
}
