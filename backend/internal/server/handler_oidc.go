package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {

	s.oidcMu.RLock()
	provider := s.oidcProvider
	s.oidcMu.RUnlock()

	if provider == nil {
		Error(w,
			http.StatusServiceUnavailable,
			"oidc_not_configured",
			"OIDC is not configured")
		return
	}

	state := uuid.New().String()

	// Add the state to whatever session the visitor already has. This endpoint
	// is an unauthenticated GET with no CSRF protection, so replacing the whole
	// session here would let any page log a visitor out with an <img> tag.
	// The decode error is ignored deliberately: CookieStore.Get always returns
	// a usable session, and an undecodable cookie simply carries no data.
	session, _ := s.sessions.Get(r, auth.SessionName)
	sd, _ := session.Values[auth.SessionDataKey].(auth.SessionData)
	req := provider.AuthorizationURL(state)

	sd.OIDCState = state
	sd.OIDCNonce = req.Nonce
	sd.OIDCCodeVerifier = req.Verifier

	if err := s.writeSession(w, r, sd); err != nil {
		handleError(w, err)
		return
	}

	http.Redirect(w, r, req.URL, http.StatusFound)
}

// loginRefused sends a browser that came back from the identity provider to
// the login page with one of our own fixed codes, never anything from the
// request. The page turns the code into text; JSON here was shown raw (#401).
func loginRefused(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(code), http.StatusSeeOther)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {

	s.oidcMu.RLock()
	provider := s.oidcProvider
	s.oidcMu.RUnlock()

	if provider == nil {
		Error(w,
			http.StatusServiceUnavailable,
			"oidc_not_configured",
			"OIDC is not configured")
		return
	}

	session, err := s.sessions.Get(r, auth.SessionName)
	if err != nil {
		handleError(w, err)
		return
	}

	sd, ok := session.Values[auth.SessionDataKey].(auth.SessionData)

	if !ok {
		loginRefused(w, r, "invalid_session")
		return
	}

	// An absent state parameter can never equal the non-empty session state, so
	// this one check covers a missing, stale and mismatched state alike.
	if sd.OIDCState == "" || r.URL.Query().Get("state") != sd.OIDCState {
		loginRefused(w, r, "invalid_state")
		return
	}

	// The state is now spent: one authorization request, one callback. fail
	// drops it from the session before reporting the failure, so a state left
	// behind by a failed callback cannot be completed later with another code.
	fail := func(code string) {
		sd.OIDCState = ""
		sd.OIDCNonce = ""
		sd.OIDCCodeVerifier = ""
		if err := s.writeSession(w, r, sd); err != nil {
			slog.Error("clearing spent OIDC state", "error", err)
		}
		loginRefused(w, r, code)
	}

	failInternal := func(err error) {
		slog.Error("OIDC callback failed", "error", err)
		fail("internal_error")
	}

	// The IdP reports a refusal (access_denied, consent_required, …) by
	// redirecting back with an error instead of a code.
	if idpErr := r.URL.Query().Get("error"); idpErr != "" {
		// The IdP's text is logged, not echoed: everything in the query string
		// is attacker-controlled.
		slog.Warn("OIDC provider returned an error",
			"error", idpErr,
			"description", r.URL.Query().Get("error_description"))
		fail("idp_error")
		return
	}

	code := r.URL.Query().Get("code")

	if code == "" {
		fail("missing_code")
		return
	}

	token, err := provider.Exchange(
		r.Context(),
		code,
		sd.OIDCCodeVerifier,
	)

	if err != nil {
		failInternal(err)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)

	if !ok {
		fail("missing_id_token")
		return
	}

	idToken, err := provider.VerifyIDToken(
		r.Context(),
		rawIDToken,
		sd.OIDCNonce,
	)

	if err != nil {
		// A nonce mismatch is logged distinctly: unlike a malformed or expired
		// token it means a token valid for something else was presented here.
		if errors.Is(err, auth.ErrNonceMismatch) {
			slog.Warn("OIDC id token nonce mismatch")
		}
		fail("invalid_id_token")
		return
	}

	var claims auth.OIDCClaims

	if err := idToken.Claims(&claims); err != nil {
		failInternal(err)
		return
	}

	email := strings.ToLower(strings.TrimSpace(claims.Email))

	// An unverified address proves nothing about who owns it, so it must not be
	// used to adopt an existing account or to provision a new one. Dropping it
	// here keeps an already-federated identity (matched on the "sub" claim)
	// logging in with its stored address untouched; a first-time login with an
	// unverified address has nothing left to go on and is refused below.
	if !claims.EmailVerified {
		email = ""
	}

	// The allowed-domain list applies to every federated login, as it does for
	// SAML JIT provisioning.
	if email != "" && !user.IsEmailDomainAllowed(email, s.adminSvc.AllowedEmailDomains(r.Context())) {
		fail("domain_not_allowed")
		return
	}

	// Same fallback chain as the SAML ACS handler, ending at the email address.
	// An IdP that releases no name claim at all is a configuration mistake, but
	// it is the operator's mistake to discover — before this chain existed the
	// login died in provisioning as "display name is required" and surfaced as
	// a 500, which reads like a server fault rather than a missing profile scope.
	name := firstNonEmpty(
		claims.Name,
		claims.PreferredUsername,
		strings.Join([]string{claims.GivenName, claims.FamilyName}, " "),
		email,
	)

	u, err := s.users.UpsertOIDCUser(
		r.Context(),
		claims.Subject,
		email,
		name,
	)

	if err != nil {
		switch {
		case errors.Is(err, user.ErrUserDisabled):
			fail("account_disabled")
		case errors.Is(err, user.ErrAccountLinkRefused):
			fail("account_link_refused")
		case errors.Is(err, user.ErrSubjectRequired):
			fail("invalid_id_token")
		case errors.Is(err, user.ErrEmailRequired):
			fail("email_not_verified")
		case errors.Is(err, user.ErrValidation):
			fail("invalid_email")
		case errors.Is(err, user.ErrDomainNotAllowed):
			fail("domain_not_allowed")
		case errors.Is(err, user.ErrEmailTaken):
			// Somebody else here already holds the address the identity
			// provider sends. That is an administrator's problem to resolve,
			// not a fault, and it used to be reported as an internal error on
			// every sign-in attempt.
			fail("email_taken")
		default:
			failInternal(err)
		}
		return
	}

	sd.UserID = u.ID
	sd.Role = u.Role
	sd.MFAPassed = true
	sd.FactorVerified = claims.AssertedMFA()
	sd.OIDCState = ""
	sd.OIDCNonce = ""
	sd.OIDCCodeVerifier = ""

	if err := s.writeSession(w, r, sd); err != nil {
		handleError(w, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
