package server_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// ── Fake identity provider ───────────────────────────────────────────────────
//
// A second copy of the fake IdP from internal/domain/auth/oidc_test.go. The two
// live in different test packages, and neither may leak helpers into a
// non-test package, so the duplication is deliberate.

type fakeOIDCClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Audience      string        // defaults to the IdP client ID
	Issuer        string        // defaults to the IdP's own URL
	Expiry        time.Duration // relative to now; defaults to +1h
	SignWithWrong bool          // sign with a key absent from the JWKS
	Nonce         string        // echoed into the id_token; the callback requires it to match
}

type fakeOIDC struct {
	srv *httptest.Server

	key      *rsa.PrivateKey
	wrongKey *rsa.PrivateKey
	keyID    string

	clientID     string
	clientSecret string

	staged string // ID token returned by the token endpoint

	// tokenRequests records the posted form of every call to /token, so a test
	// can assert on what was actually sent on the back channel.
	tokenRequests []url.Values
}

func newFakeOIDC(t *testing.T) *fakeOIDC {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	idp := &fakeOIDC{
		key:          key,
		wrongKey:     wrongKey,
		keyID:        "srv-test-key",
		clientID:     "srv-test-client",
		clientSecret: "srv-test-client-secret",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer(),
			"authorization_endpoint":                idp.issuer() + "/authorize",
			"token_endpoint":                        idp.issuer() + "/token",
			"jwks_uri":                              idp.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": idp.keyID,
				"n":   b64url(idp.key.N.Bytes()),
				"e":   b64url(big.NewInt(int64(idp.key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err == nil {
			idp.tokenRequests = append(idp.tokenRequests, r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "srv-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.staged,
		})
	})

	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *fakeOIDC) issuer() string { return i.srv.URL }

func (i *fakeOIDC) mint(t *testing.T, c fakeOIDCClaims) string {
	t.Helper()

	iss := c.Issuer
	if iss == "" {
		iss = i.issuer()
	}
	aud := c.Audience
	if aud == "" {
		aud = i.clientID
	}
	exp := c.Expiry
	if exp == 0 {
		exp = time.Hour
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":            iss,
		"aud":            aud,
		"sub":            c.Subject,
		"iat":            time.Now().Add(-time.Minute).Unix(),
		"exp":            time.Now().Add(exp).Unix(),
		"email":          c.Email,
		"email_verified": c.EmailVerified,
		"name":           c.Name,
	})
	// Omitted entirely when unset, so a test can mint a token carrying no nonce
	// at all and confirm the callback refuses it.
	if c.Nonce != "" {
		tok.Claims.(jwt.MapClaims)["nonce"] = c.Nonce
	}
	tok.Header["kid"] = i.keyID

	signing := i.key
	if c.SignWithWrong {
		signing = i.wrongKey
	}
	raw, err := tok.SignedString(signing)
	require.NoError(t, err)
	return raw
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ── Harness ──────────────────────────────────────────────────────────────────

type oidcHarness struct {
	*harness
	idp *fakeOIDC
}

// newOIDCHarness builds the standard integration harness, points it at a fake
// IdP, and initialises the in-memory OIDC provider.
func newOIDCHarness(t *testing.T) (*oidcHarness, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
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

	return &oidcHarness{harness: h, idp: idp}, cleanup
}

// rawGet issues an unauthenticated GET, attaching the given cookies.
func (h *harness) rawGet(t *testing.T, path string, cookies []*http.Cookie) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// rawPostJSON issues an unauthenticated POST with a JSON body.
func (h *harness) rawPostJSON(t *testing.T, path string, body any, cookies []*http.Cookie) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// startOIDCLogin performs GET /auth/oidc/login and returns the state parameter
// handed to the IdP plus the session cookies the server set.
func (oh *oidcHarness) startOIDCLogin(t *testing.T, cookies []*http.Cookie) (string, []*http.Cookie) {
	t.Helper()
	state, _, out := oh.startOIDCLoginFull(t, cookies)
	return state, out
}

// startOIDCLoginFull additionally returns the nonce the server sent to the IdP,
// which a caller must echo back in the id_token for the callback to accept it.
func (oh *oidcHarness) startOIDCLoginFull(t *testing.T, cookies []*http.Cookie) (state, nonce string, out []*http.Cookie) {
	t.Helper()
	resp := oh.rawGet(t, "/api/v1/auth/oidc/login", cookies)
	require.Equal(t, http.StatusFound, resp.StatusCode)

	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	state = loc.Query().Get("state")
	require.NotEmpty(t, state, "login must send a state parameter")
	nonce = loc.Query().Get("nonce")
	require.NotEmpty(t, nonce, "login must send a nonce parameter")

	out = resp.Cookies()
	require.NotEmpty(t, out, "login must set the session cookie holding the state")
	return state, nonce, out
}

// callback drives GET /auth/oidc/callback with the given query parameters.
func (oh *oidcHarness) callback(t *testing.T, q url.Values, cookies []*http.Cookie) *http.Response {
	t.Helper()
	return oh.rawGet(t, "/api/v1/auth/oidc/callback?"+q.Encode(), cookies)
}

// login runs a full, successful-looking OIDC round trip with the given claims
// and returns the callback response.
func (oh *oidcHarness) login(t *testing.T, claims fakeOIDCClaims) *http.Response {
	t.Helper()
	state, nonce, cookies := oh.startOIDCLoginFull(t, nil)
	// A conforming IdP echoes the nonce it was given; tests that want the
	// non-conforming case set claims.Nonce themselves and call the pieces.
	if claims.Nonce == "" {
		claims.Nonce = nonce
	}
	oh.idp.staged = oh.idp.mint(t, claims)
	return oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, cookies)
}

// errorCode pulls the code out of the standard error envelope.
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = json.Unmarshal(raw, &body)
	return body.Error.Code
}

// findUser returns the user with the given email, or ok=false.
func findUser(t *testing.T, h *harness, email string) (user.User, bool) {
	t.Helper()
	users, err := h.userSvc.ListAdmin(context.Background(), 500, 0)
	require.NoError(t, err)
	for _, u := range users {
		if strings.EqualFold(u.Email, email) {
			return u, true
		}
	}
	return user.User{}, false
}

// whoami reports the user behind the given cookies, if they authenticate at all.
func whoami(t *testing.T, h *harness, cookies []*http.Cookie) (user.User, bool) {
	t.Helper()
	resp := h.rawGet(t, "/api/v1/me", cookies)
	if resp.StatusCode != http.StatusOK {
		return user.User{}, false
	}
	var u user.User
	decodeJSON(t, resp, &u)
	return u, true
}

// ── Not configured ───────────────────────────────────────────────────────────

func TestOIDC_NotConfigured(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, path := range []string{
		"/api/v1/auth/oidc/login",
		"/api/v1/auth/oidc/callback?state=x&code=y",
	} {
		t.Run(path, func(t *testing.T) {
			resp := h.rawGet(t, path, nil)
			require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
			require.Equal(t, "oidc_not_configured", errorCode(t, resp))
		})
	}
}

// ── Login ────────────────────────────────────────────────────────────────────

func TestOIDCLogin_RedirectsToIdP(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.rawGet(t, "/api/v1/auth/oidc/login", nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)

	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, oh.idp.issuer()+"/authorize", loc.Scheme+"://"+loc.Host+loc.Path)

	q := loc.Query()
	require.NotEmpty(t, q.Get("state"))
	require.Equal(t, oh.idp.clientID, q.Get("client_id"))
	require.Contains(t, strings.Fields(q.Get("scope")), "openid")

	require.NotEmpty(t, resp.Cookies(), "the state must be persisted in the session cookie")
}

// TestOIDCLogin_PreservesExistingSession pins defect 8.
//
// handleOIDCLogin writes auth.SessionData{OIDCState: state}, replacing the whole
// session struct and zeroing UserID/Role/MFAPassed. GET is unauthenticated and
// has no CSRF protection, so `<img src=".../auth/oidc/login">` on any page
// silently logs the visitor out. Starting an OIDC login must add the state to
// the existing session, not overwrite it.
func TestOIDCLogin_PreservesExistingSession(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	// Log in locally as the admin and confirm the session works.
	resp := oh.rawPostJSON(t, "/api/v1/auth/local/login", map[string]string{
		"email":    "admin@test.local",
		"password": "password",
	}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sessionCookies := resp.Cookies()
	require.NotEmpty(t, sessionCookies)

	before, ok := whoami(t, oh.harness, sessionCookies)
	require.True(t, ok, "precondition: the local session must authenticate")
	require.Equal(t, oh.adminID, before.ID)

	// A visitor's browser is made to hit the OIDC login endpoint.
	_, afterCookies := oh.startOIDCLogin(t, sessionCookies)

	after, ok := whoami(t, oh.harness, afterCookies)
	require.True(t, ok, "starting an OIDC login must not log the visitor out")
	require.Equal(t, before.ID, after.ID, "the existing session must survive")
}

// ── Callback: state and code handling ────────────────────────────────────────

func TestOIDCCallback_StateMismatch(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	_, cookies := oh.startOIDCLogin(t, nil)

	resp := oh.callback(t, url.Values{"state": {"not-the-state"}, "code": {"c"}}, cookies)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_state", errorCode(t, resp))
}

func TestOIDCCallback_NoSessionCookie(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.callback(t, url.Values{"state": {"whatever"}, "code": {"c"}}, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_session", errorCode(t, resp))
}

func TestOIDCCallback_MissingCode(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	state, cookies := oh.startOIDCLogin(t, nil)

	resp := oh.callback(t, url.Values{"state": {state}}, cookies)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Equal(t, "missing_code", errorCode(t, resp))
}

// TestOIDCCallback_MissingStateParamIsReportedAsInvalidState records the other
// half of defect 10: the `state == ""` / "missing_state" branch in
// handleOIDCCallback sits *after* the state-equality check, so it can never run —
// an absent state parameter can never equal the non-empty session state. The
// branch is dead code and should be deleted (or moved above the comparison).
//
// This test PASSES today and documents the reachable behaviour. If a fix moves
// the emptiness check earlier, the expected code becomes "missing_state".
func TestOIDCCallback_MissingStateParamIsReportedAsInvalidState(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	_, cookies := oh.startOIDCLogin(t, nil)

	resp := oh.callback(t, url.Values{"code": {"the-code"}}, cookies)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_state", errorCode(t, resp),
		"the missing_state branch is unreachable dead code")
}

// TestOIDCCallback_IdPErrorResponse pins defect 10.
//
// When the IdP refuses, it redirects back with ?error=access_denied and no code.
// The handler only looks for a missing code, so a user who clicked "Deny" — or
// hit consent_required, login_required, or an IdP-side failure — is told
// "OIDC authorization code missing", which is both wrong and undebuggable.
func TestOIDCCallback_IdPErrorResponse(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	state, cookies := oh.startOIDCLogin(t, nil)

	resp := oh.callback(t, url.Values{
		"state":             {state},
		"error":             {"access_denied"},
		"error_description": {"user denied consent"},
	}, cookies)

	require.GreaterOrEqual(t, resp.StatusCode, 400)
	require.Less(t, resp.StatusCode, 500)

	code := errorCode(t, resp)
	require.NotEqual(t, "missing_code", code,
		"an IdP error response must be reported as such, not as a missing code")
	require.NotEmpty(t, code)
}

// TestOIDCCallback_StateIsNotReplayable pins the second half of defect 8.
//
// The callback clears OIDCState only on the success path. After any failure the
// state stays in the session, so the same state (and therefore the same
// authorization request) can be completed later — including by a different
// request that smuggles in its own code.
func TestOIDCCallback_StateIsNotReplayable(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	state, cookies := oh.startOIDCLogin(t, nil)

	// First attempt fails: no code.
	failed := oh.callback(t, url.Values{"state": {state}}, cookies)
	require.Equal(t, http.StatusBadRequest, failed.StatusCode)

	// The session cookie is unchanged by the failure, so reuse it.
	replayCookies := cookies
	if c := failed.Cookies(); len(c) > 0 {
		replayCookies = c
	}

	oh.idp.staged = oh.idp.mint(t, fakeOIDCClaims{
		Subject:       "replay-sub",
		Email:         "replay@test.local",
		EmailVerified: true,
		Name:          "Replay",
	})
	replay := oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, replayCookies)

	require.NotEqual(t, http.StatusSeeOther, replay.StatusCode,
		"a state consumed by a failed callback must not be accepted again")
	if _, ok := whoami(t, oh.harness, replay.Cookies()); ok {
		t.Fatal("a replayed state must not produce an authenticated session")
	}
}

// ── Callback: token validation ───────────────────────────────────────────────

func TestOIDCCallback_TokenValidation(t *testing.T) {
	cases := []struct {
		name   string
		claims fakeOIDCClaims
	}{
		{
			name: "signature from an unpublished key",
			claims: fakeOIDCClaims{
				Subject: "bad-sig", Email: "badsig@test.local",
				EmailVerified: true, SignWithWrong: true,
			},
		},
		{
			name: "audience is another client",
			claims: fakeOIDCClaims{
				Subject: "bad-aud", Email: "badaud@test.local",
				EmailVerified: true, Audience: "someone-else",
			},
		},
		{
			name: "token is expired",
			claims: fakeOIDCClaims{
				Subject: "expired", Email: "expired@test.local",
				EmailVerified: true, Expiry: -time.Hour,
			},
		},
		{
			name: "issuer is not the configured one",
			claims: fakeOIDCClaims{
				Subject: "bad-iss", Email: "badiss@test.local",
				EmailVerified: true, Issuer: "https://evil.example",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oh, cleanup := newOIDCHarness(t)
			defer cleanup()

			resp := oh.login(t, tc.claims)
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			require.Equal(t, "invalid_id_token", errorCode(t, resp))

			_, ok := findUser(t, oh.harness, tc.claims.Email)
			require.False(t, ok, "a rejected token must not create a user")
		})
	}
}

// ── Callback: provisioning ───────────────────────────────────────────────────

// TestOIDCCallback_NewUserGetsRoleUser pins defect 1: UpsertOIDCUser hard-codes
// Role: RoleStaff, so anyone the IdP will authenticate gets agent-level access
// to every ticket. UpsertSAMLUser creates RoleUser; OIDC must match.
func TestOIDCCallback_NewUserGetsRoleUser(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "fresh-sub",
		Email:         "newbie@test.local",
		EmailVerified: true,
		Name:          "Newbie",
	})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)

	u, ok := findUser(t, oh.harness, "newbie@test.local")
	require.True(t, ok, "the user should have been provisioned")
	require.Equal(t, user.RoleUser, u.Role,
		"a just-in-time federated account must be a plain user")
}

// TestOIDCCallback_DomainNotAllowed pins defect 2: the OIDC path never consults
// the allowed_email_domains setting, so a domain restriction that holds for SAML
// is silently bypassed by OIDC.
func TestOIDCCallback_DomainNotAllowed(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	require.NoError(t, oh.adminSvc.SetRaw(context.Background(),
		admin.KeyAllowedEmailDomains, []byte(`["allowed.test"]`)))

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "outsider-sub",
		Email:         "outsider@notallowed.test",
		EmailVerified: true,
		Name:          "Outsider",
	})

	_, ok := findUser(t, oh.harness, "outsider@notallowed.test")
	require.False(t, ok, "a disallowed email domain must not be provisioned")

	require.NotEqual(t, http.StatusSeeOther, resp.StatusCode,
		"a disallowed domain must not complete the login")
	if _, authed := whoami(t, oh.harness, resp.Cookies()); authed {
		t.Fatal("a disallowed domain must not produce an authenticated session")
	}
}

// TestOIDCCallback_UnverifiedEmailDoesNotLink pins defect 3.
//
// OIDCClaims.EmailVerified is decoded and then never read. When the sub is
// unknown, UpsertOIDCUser looks the account up by email and binds the subject to
// it — so an IdP account holder who merely *claims* someone else's address
// inherits that account.
func TestOIDCCallback_UnverifiedEmailDoesNotLink(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	victim, ok := findUser(t, oh.harness, "user@test.local")
	require.True(t, ok)
	require.Empty(t, victim.OIDCSubject, "precondition: the account is not federated")

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "impostor-sub",
		Email:         "user@test.local",
		EmailVerified: false,
		Name:          "Impostor",
	})

	after, err := oh.userSvc.GetByIDAdmin(context.Background(), victim.ID)
	require.NoError(t, err)
	require.Empty(t, after.OIDCSubject,
		"an unverified email must not bind an OIDC subject to an existing account")

	if u, authed := whoami(t, oh.harness, resp.Cookies()); authed {
		require.NotEqual(t, victim.ID, u.ID,
			"an unverified email must not authenticate as the existing account")
	}
}

// TestOIDCCallback_DoesNotLinkOntoAdmin pins defect 3 at its worst: the
// email-fallback link has no role guard, so a token for the admin's address —
// verified or not — grants the admin account.
func TestOIDCCallback_DoesNotLinkOntoAdmin(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "attacker-sub",
		Email:         "admin@test.local",
		EmailVerified: true,
		Name:          "Definitely The Admin",
	})

	adminAfter, err := oh.userSvc.GetByIDAdmin(context.Background(), oh.adminID)
	require.NoError(t, err)
	require.Empty(t, adminAfter.OIDCSubject,
		"an unknown OIDC subject must never be bound to an admin account")

	if u, authed := whoami(t, oh.harness, resp.Cookies()); authed {
		require.NotEqual(t, oh.adminID, u.ID, "the callback must not hand out the admin session")
		require.NotEqual(t, user.RoleAdmin, u.Role)
	}
}

// TestOIDCCallback_DisabledUserGetsNoSession pins defect 4.
//
// VerifyPassword refuses !u.IsActive(); the OIDC path never checks, and
// GetUserByOIDCSubject filters only deleted_at. Disabling an account therefore
// does not stop the holder logging in through the IdP.
func TestOIDCCallback_DisabledUserGetsNoSession(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	ctx := context.Background()
	u, err := oh.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "suspended@test.local",
		DisplayName: "Suspended",
		Role:        user.RoleUser,
		OIDCSubject: "suspended-sub",
	})
	require.NoError(t, err)
	require.NoError(t, oh.userSvc.Disable(ctx, u.ID))

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "suspended-sub",
		Email:         "suspended@test.local",
		EmailVerified: true,
		Name:          "Suspended",
	})

	require.NotEqual(t, http.StatusSeeOther, resp.StatusCode,
		"a disabled account must not complete an OIDC login")
	if _, authed := whoami(t, oh.harness, resp.Cookies()); authed {
		t.Fatal("a disabled account must not end up with an authenticated session")
	}
}

// TestOIDCCallback_WrongNonceIsRejected covers the attack the nonce exists to
// stop: an ID token that is genuinely signed by the IdP, in date, and for the
// right audience, but was minted for a different login. Every other check in
// the callback passes it.
func TestOIDCCallback_WrongNonceIsRejected(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	state, _, cookies := oh.startOIDCLoginFull(t, nil)

	// Valid in every respect except that it answers a different authorization
	// request than the one this session started.
	oh.idp.staged = oh.idp.mint(t, fakeOIDCClaims{
		Subject:       "attacker-sub",
		Email:         "attacker@test.local",
		EmailVerified: true,
		Nonce:         "nonce-from-some-other-login",
	})

	resp := oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, cookies)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_id_token", errorCode(t, resp))

	_, ok := whoami(t, oh.harness, resp.Cookies())
	require.False(t, ok, "a token minted for another login must not create a session")
}

// TestOIDCCallback_MissingNonceIsRejected is the degenerate case: an IdP that
// drops the nonce claim entirely must not be treated as a match. Comparing a
// stored nonce against an absent one has to fail, or the check is decorative.
func TestOIDCCallback_MissingNonceIsRejected(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	state, _, cookies := oh.startOIDCLoginFull(t, nil)

	// Nonce deliberately left empty, so mint omits the claim altogether.
	oh.idp.staged = oh.idp.mint(t, fakeOIDCClaims{
		Subject:       "no-nonce-sub",
		Email:         "nononce@test.local",
		EmailVerified: true,
	})

	resp := oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, cookies)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, "invalid_id_token", errorCode(t, resp))

	_, ok := whoami(t, oh.harness, resp.Cookies())
	require.False(t, ok, "an id_token with no nonce must not create a session")
}

// TestOIDCLogin_SendsPKCEChallenge checks the front-channel half of PKCE from
// the handler's side, and that the verifier itself never appears in the
// redirect — only its S256 hash may travel over the front channel.
func TestOIDCLogin_SendsPKCEChallenge(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.rawGet(t, "/api/v1/auth/oidc/login", nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)

	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	q := loc.Query()

	require.NotEmpty(t, q.Get("code_challenge"), "PKCE challenge must be sent")
	require.Equal(t, "S256", q.Get("code_challenge_method"), "plain PKCE is not acceptable")
	require.Empty(t, q.Get("code_verifier"), "the verifier must never travel the front channel")
}

// TestOIDCCallback_SendsCodeVerifierOnExchange is the back-channel half: the
// verifier stored at login must reach the token endpoint, and must be the
// preimage of the challenge sent earlier. Without this the challenge is theatre.
func TestOIDCCallback_SendsCodeVerifierOnExchange(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.rawGet(t, "/api/v1/auth/oidc/login", nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)

	state := loc.Query().Get("state")
	nonce := loc.Query().Get("nonce")
	challenge := loc.Query().Get("code_challenge")
	cookies := resp.Cookies()

	oh.idp.staged = oh.idp.mint(t, fakeOIDCClaims{
		Subject:       "pkce-sub",
		Email:         "pkce@test.local",
		EmailVerified: true,
		Name:          "PKCE User",
		Nonce:         nonce,
	})

	cb := oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, cookies)
	require.Equal(t, http.StatusSeeOther, cb.StatusCode)

	require.NotEmpty(t, oh.idp.tokenRequests, "the token endpoint must have been called")
	form := oh.idp.tokenRequests[len(oh.idp.tokenRequests)-1]

	verifier := form.Get("code_verifier")
	require.NotEmpty(t, verifier, "the PKCE verifier must be sent on exchange")
	require.Equal(t, challenge, oauth2.S256ChallengeFromVerifier(verifier),
		"the verifier sent at exchange must be the preimage of the challenge sent at login")
}

func TestOIDCCallback_HappyPathCreatesSession(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.login(t, fakeOIDCClaims{
		Subject:       "happy-sub",
		Email:         "happy@test.local",
		EmailVerified: true,
		Name:          "Happy Path",
	})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/", resp.Header.Get("Location"))

	u, authed := whoami(t, oh.harness, resp.Cookies())
	require.True(t, authed, "a successful OIDC login must authenticate the session")
	require.Equal(t, "happy@test.local", u.Email)
	require.Equal(t, "Happy Path", u.DisplayName)
}

// ── Admin surface ────────────────────────────────────────────────────────────

// TestGetSettings_DoesNotLeakOIDCClientSecret pins defect 7: GET /admin/oidc
// deliberately blanks client_secret, and GET /admin/settings then dumps the
// whole settings table — secret included — defeating the point.
func TestGetSettings_DoesNotLeakOIDCClientSecret(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.NotContains(t, string(raw), oh.idp.clientSecret,
		"the OIDC client secret must never be returned by the settings endpoint")

	var settings map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &settings))
	require.NotContains(t, settings, admin.KeyOIDCClientSecret,
		"oidc_client_secret must be redacted from the settings dump")
}

func TestGetOIDCConfig_NeverReturnsClientSecret(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oidc", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	decodeJSON(t, resp, &body)
	require.Equal(t, "", body["client_secret"])
	require.Equal(t, true, body["configured"])
	require.Equal(t, oh.idp.clientID, body["client_id"])
}

func TestSaveOIDCConfig_BlankSecretPreservesStored(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	resp := oh.doAsAdmin(t, http.MethodPut, "/api/v1/admin/oidc", map[string]any{
		"enabled":       true,
		"issuer_url":    oh.idp.issuer(),
		"client_id":     "rotated-client-id",
		"client_secret": "",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cfg := oh.adminSvc.GetOIDCConfig(context.Background())
	require.Equal(t, "rotated-client-id", cfg.ClientID)
	require.Equal(t, oh.idp.clientSecret, cfg.ClientSecret,
		"a blank client_secret must preserve the stored one")
}
