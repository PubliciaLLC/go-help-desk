package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// ── Fake identity provider ───────────────────────────────────────────────────

// idpClaims is the configurable claim set for a minted ID token.
type idpClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Nonce         string
	Audience      string        // defaults to the IdP's client ID
	Issuer        string        // defaults to the IdP's own URL
	Expiry        time.Duration // relative to now; defaults to +1h
	SignWithWrong bool          // sign with a key that is not published in the JWKS
}

// fakeIDP is a minimal OpenID Connect provider: discovery document, JWKS, and
// a token endpoint that returns whatever ID token the test last staged.
type fakeIDP struct {
	srv *httptest.Server

	key      *rsa.PrivateKey
	wrongKey *rsa.PrivateKey
	keyID    string

	clientID     string
	clientSecret string

	// staged is the ID token handed back by the token endpoint.
	staged string

	// tokenRequests records the form values of every token exchange.
	tokenRequests []url.Values
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	idp := &fakeIDP{
		key:          key,
		wrongKey:     wrongKey,
		keyID:        "test-key-1",
		clientID:     "test-client-id",
		clientSecret: "test-client-secret",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer(),
			"authorization_endpoint":                idp.issuer() + "/authorize",
			"token_endpoint":                        idp.issuer() + "/token",
			"jwks_uri":                              idp.issuer() + "/keys",
			"userinfo_endpoint":                     idp.issuer() + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"scopes_supported":                      []string{"openid", "profile", "email"},
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
				"n":   b64u(idp.key.N.Bytes()),
				"e":   b64u(big.NewInt(int64(idp.key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.tokenRequests = append(idp.tokenRequests, r.Form)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idp.staged,
		})
	})

	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (i *fakeIDP) issuer() string { return i.srv.URL }

func (i *fakeIDP) config(redirectURL string) auth.OIDCConfig {
	return auth.OIDCConfig{
		Enabled:      true,
		IssuerURL:    i.issuer(),
		ClientID:     i.clientID,
		ClientSecret: i.clientSecret,
		RedirectURL:  redirectURL,
	}
}

// mint builds and signs an ID token with the given claims.
func (i *fakeIDP) mint(t *testing.T, c idpClaims) string {
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

	claims := jwt.MapClaims{
		"iss":            iss,
		"aud":            aud,
		"sub":            c.Subject,
		"iat":            time.Now().Add(-time.Minute).Unix(),
		"exp":            time.Now().Add(exp).Unix(),
		"email":          c.Email,
		"email_verified": c.EmailVerified,
		"name":           c.Name,
	}
	if c.Nonce != "" {
		claims["nonce"] = c.Nonce
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = i.keyID

	signing := i.key
	if c.SignWithWrong {
		signing = i.wrongKey
	}
	raw, err := tok.SignedString(signing)
	require.NoError(t, err)
	return raw
}

// stage makes the token endpoint return this ID token on the next exchange.
func (i *fakeIDP) stage(raw string) { i.staged = raw }

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ── Discovery ────────────────────────────────────────────────────────────────

func TestNewOIDCProvider_Success(t *testing.T) {
	idp := newFakeIDP(t)

	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)
	require.NotNil(t, p)
	require.NotNil(t, p.Config)
	require.NotNil(t, p.Verifier)
	require.Equal(t, idp.clientID, p.Config.ClientID)
	require.Equal(t, idp.issuer()+"/authorize", p.Config.Endpoint.AuthURL)
	require.Equal(t, idp.issuer()+"/token", p.Config.Endpoint.TokenURL)
	require.Equal(t, []string{"openid", "profile", "email"}, p.Config.Scopes)
}

func TestNewOIDCProvider_DiscoveryFailure(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "discovery endpoint returns 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
		},
		{
			name: "discovery document is not JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("not json"))
			},
		},
		{
			name: "issuer in the document does not match the configured issuer",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 "https://evil.example",
					"authorization_endpoint": "https://evil.example/authorize",
					"token_endpoint":         "https://evil.example/token",
					"jwks_uri":               "https://evil.example/keys",
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			p, err := auth.NewOIDCProvider(context.Background(), auth.OIDCConfig{
				Enabled:      true,
				IssuerURL:    srv.URL,
				ClientID:     "cid",
				ClientSecret: "secret",
			})
			require.Error(t, err)
			require.Nil(t, p)
		})
	}
}

// TestNewOIDCProvider_DiscoveryErrorIsWrapped asserts the convention in
// .claude/CLAUDE.md — "Wrap errors with context" — on the discovery failure
// path. NewOIDCProvider currently returns go-oidc's bare error, which on an
// HTTP error reads like "500 Internal Server Error: boom" with nothing naming
// OIDC or the issuer, so an operator reading the log cannot tell what failed.
func TestNewOIDCProvider_DiscoveryErrorIsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := auth.NewOIDCProvider(context.Background(), auth.OIDCConfig{
		Enabled:      true,
		IssuerURL:    srv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	})
	require.Error(t, err)
	msg := strings.ToLower(err.Error())
	require.True(t,
		strings.Contains(msg, "oidc") || strings.Contains(msg, strings.ToLower(srv.URL)),
		"discovery error must be wrapped with context identifying OIDC or the issuer, got: %s", err)
}

// ── Authorization URL ────────────────────────────────────────────────────────

func TestAuthorizationURL(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/api/v1/auth/oidc/callback"))
	require.NoError(t, err)

	req := p.AuthorizationURL("state-abc-123")
	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	q := u.Query()

	require.Equal(t, idp.issuer()+"/authorize", u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, "state-abc-123", q.Get("state"))
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, idp.clientID, q.Get("client_id"))
	require.Equal(t, "http://app.test/api/v1/auth/oidc/callback", q.Get("redirect_uri"))

	scopes := strings.Fields(q.Get("scope"))
	require.Contains(t, scopes, "openid")
	require.Contains(t, scopes, "profile")
	require.Contains(t, scopes, "email")
}

// TestAuthorizationURL_SendsNonceAndPKCE is the inverted form of the tripwire
// that used to record defect 9. It previously asserted that nonce, PKCE and a
// pointless offline-access request were all present-or-absent as shipped; the
// assertions below are its opposite, and this test failing means the hardening
// has been backed out.
func TestAuthorizationURL_SendsNonceAndPKCE(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	req := p.AuthorizationURL("state-abc-123")
	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	q := u.Query()

	require.NotEmpty(t, req.Nonce, "a nonce must be generated for the caller to store")
	require.Equal(t, req.Nonce, q.Get("nonce"),
		"the nonce sent to the IdP must be the one handed back to the caller")

	require.NotEmpty(t, req.Verifier, "a PKCE verifier must be generated for the caller to store")
	require.Equal(t, "S256", q.Get("code_challenge_method"),
		"plain PKCE is not acceptable; the challenge must be S256")
	require.Equal(t, oauth2.S256ChallengeFromVerifier(req.Verifier), q.Get("code_challenge"),
		"the challenge must be the S256 hash of the verifier handed back to the caller")

	// The verifier itself must never appear in the front-channel redirect: the
	// whole point is that only the client can present it at exchange time.
	require.NotContains(t, req.URL, req.Verifier,
		"the PKCE verifier must not leak into the authorization URL")

	require.Empty(t, q.Get("access_type"),
		"offline access must not be requested: no refresh token is stored or used")
}

// TestAuthorizationURL_IsUniquePerCall guards against a nonce or verifier that
// is constant across logins, which would defeat both mechanisms while leaving
// every other assertion in this file passing.
func TestAuthorizationURL_IsUniquePerCall(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	a := p.AuthorizationURL("state-1")
	b := p.AuthorizationURL("state-2")

	require.NotEqual(t, a.Nonce, b.Nonce, "nonce must be fresh per authorization request")
	require.NotEqual(t, a.Verifier, b.Verifier, "PKCE verifier must be fresh per authorization request")
}

// ── ID token verification ────────────────────────────────────────────────────

func TestVerifyIDToken(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	cases := []struct {
		name    string
		claims  idpClaims
		wantErr bool
	}{
		{
			name: "valid token is accepted",
			claims: idpClaims{
				Subject:       "sub-1",
				Email:         "user@example.com",
				EmailVerified: true,
				Name:          "Example User",
			},
		},
		{
			name: "wrong audience is rejected",
			claims: idpClaims{
				Subject:  "sub-1",
				Email:    "user@example.com",
				Audience: "some-other-client",
			},
			wantErr: true,
		},
		{
			name: "expired token is rejected",
			claims: idpClaims{
				Subject: "sub-1",
				Email:   "user@example.com",
				Expiry:  -time.Hour,
			},
			wantErr: true,
		},
		{
			name: "token signed with an unpublished key is rejected",
			claims: idpClaims{
				Subject:       "sub-1",
				Email:         "user@example.com",
				SignWithWrong: true,
			},
			wantErr: true,
		},
		{
			name: "wrong issuer is rejected",
			claims: idpClaims{
				Subject: "sub-1",
				Email:   "user@example.com",
				Issuer:  "https://evil.example",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.claims.Nonce = "nonce-for-this-login"
			raw := idp.mint(t, tc.claims)

			tok, err := p.VerifyIDToken(context.Background(), raw, "nonce-for-this-login")
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.claims.Subject, tok.Subject)
		})
	}
}

func TestVerifyIDToken_GarbageToken(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	_, err = p.VerifyIDToken(context.Background(), "not.a.jwt", "any-nonce")
	require.Error(t, err)
}

// TestDecodeClaims documents that EmailVerified is decoded off the ID token —
// the value is available to callers, it is simply never consulted (defect 3).
func TestDecodeClaims(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	for _, verified := range []bool{true, false} {
		raw := idp.mint(t, idpClaims{
			Subject:       "sub-claims",
			Email:         "claims@example.com",
			EmailVerified: verified,
			Name:          "Claims User",
			Nonce:         "claims-nonce",
		})
		tok, err := p.VerifyIDToken(context.Background(), raw, "claims-nonce")
		require.NoError(t, err)

		claims, err := auth.DecodeClaims(tok)
		require.NoError(t, err)
		require.Equal(t, "sub-claims", claims.Subject)
		require.Equal(t, "claims@example.com", claims.Email)
		require.Equal(t, "Claims User", claims.Name)
		require.Equal(t, verified, claims.EmailVerified)
	}
}

// ── Token exchange ───────────────────────────────────────────────────────────

func TestExchange(t *testing.T) {
	idp := newFakeIDP(t)
	p, err := auth.NewOIDCProvider(context.Background(), idp.config("http://app.test/callback"))
	require.NoError(t, err)

	idp.stage(idp.mint(t, idpClaims{Subject: "sub-x", Email: "x@example.com", EmailVerified: true}))

	tok, err := p.Exchange(context.Background(), "the-code", "the-verifier")
	require.NoError(t, err)
	require.Equal(t, "test-access-token", tok.AccessToken)

	rawIDToken, ok := tok.Extra("id_token").(string)
	require.True(t, ok, "token response must carry an id_token")
	require.NotEmpty(t, rawIDToken)

	require.Len(t, idp.tokenRequests, 1)
	form := idp.tokenRequests[0]
	require.Equal(t, "authorization_code", form.Get("grant_type"))
	require.Equal(t, "the-code", form.Get("code"))

	// Inverted form of the exchange-side tripwire for defect 9: the verifier
	// must reach the token endpoint, or PKCE proves nothing.
	require.Equal(t, "the-verifier", form.Get("code_verifier"),
		"the PKCE code_verifier must be sent on exchange")
}
