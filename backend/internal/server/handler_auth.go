package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/crewjam/saml/samlsp"
	"github.com/gorilla/sessions"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// POST /api/v1/auth/local/login
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// The password is checked FIRST, and a correct one is honoured even when
	// the budget is spent.
	//
	// Refusing on the count before verifying — which this did — let any
	// anonymous caller lock any account out of its own login just by knowing
	// the email address and sending wrong guesses. Verified: three guesses,
	// then the real user's correct password answered 429. That is the very
	// property this change criticised in the address-keyed version, on a key
	// that is far easier to learn than an IP.
	//
	// Ordering it this way costs nothing real. bcrypt is what actually limits
	// password guessing — every attempt burns tens of milliseconds of server
	// CPU whatever the counter says — so the counter's job is to stop sustained
	// wrong guessing, not to be the primary cost. It cannot be used to deny
	// anyone their own account, because the correct password always works.
	//
	// (TOTP is the opposite case: verification is a cheap HMAC, so there the
	// count has to gate before the check. It does — see CheckMFALock below.)
	loginKey := "login:" + loginRateKey(body.Email)

	u, err := s.users.VerifyPassword(r.Context(), body.Email, body.Password)
	if err != nil {
		// Only failures are counted, and the refusal happens here rather than
		// before the check, so a legitimate user is never held out.
		if !s.loginLimiter.Allow(loginKey) {
			tooManyAttempts(w, time.Minute)
			return
		}
		Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	if !user.IsLocalAuthAllowed(u, s.adminSvc.SAMLEnabled(r.Context())) {
		Error(w, http.StatusForbidden, "saml_required", "local login is disabled; use SAML")
		return
	}

	// MFA gate: three outcomes after a valid password.
	//   - enrolled user & MFA enabled → must verify TOTP (mfa_needed)
	//   - not enrolled & role is in enforced list → must enroll before access (mfa_enrollment_needed)
	//   - otherwise → session is fully authenticated
	mfaEnabled := s.adminSvc.MFAEnabled(r.Context())
	// The password was right, so prior failures for this address stop counting.
	s.loginLimiter.Reset(loginKey)

	mfaNeeded := mfaEnabled && u.MFAEnabled
	mfaEnrollmentNeeded := mfaEnabled && !u.MFAEnabled && s.adminSvc.MFARequiredFor(r.Context(), string(u.Role))
	mfaPassed := !mfaNeeded && !mfaEnrollmentNeeded

	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    u.ID,
		Role:      u.Role,
		MFAPassed: mfaPassed,
	}); err != nil {
		handleError(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user":                  u,
		"mfa_needed":            mfaNeeded,
		"mfa_enrollment_needed": mfaEnrollmentNeeded,
	})
}

// POST /api/v1/auth/local/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, _ := s.sessions.Get(r, auth.SessionName)
	session.Options = &sessions.Options{MaxAge: -1}
	_ = session.Save(r, w)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/local/mfa/verify
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a == nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "not logged in")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Counted on the user row, not in memory: a counter a restart clears is
	// not a limit on a six-digit secret, and per-process counters multiply by
	// the replica count. Keyed on the account under attack, so no proxy
	// topology obscures it.
	//
	// There is no anonymous lockout vector: reaching this endpoint requires a
	// session that already passed the password, so burning someone's budget
	// means you already hold their password — an alarm, not a denial of
	// service.
	if err := s.users.CheckMFALock(r.Context(), a.UserID); err != nil {
		if errors.Is(err, user.ErrMFALocked) {
			tooManyAttempts(w, user.MFALockDuration)
			return
		}
		handleError(w, err)
		return
	}

	if err := s.users.VerifyMFACode(r.Context(), a.UserID, body.Code); err != nil {
		if lockErr := s.users.RecordMFAFailure(r.Context(), a.UserID); lockErr != nil && !errors.Is(lockErr, user.ErrMFALocked) {
			handleError(w, lockErr)
			return
		}
		Error(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid TOTP code")
		return
	}
	if err := s.users.ClearMFAFailures(r.Context(), a.UserID); err != nil {
		handleError(w, err)
		return
	}
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    a.UserID,
		Role:      a.Role,
		MFAPassed: true,
	}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/oauth/token
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.GrantType != "client_credentials" {
		Error(w, http.StatusBadRequest, "unsupported_grant_type", "only client_credentials is supported")
		return
	}

	client, err := s.oauthClientStore.GetByClientID(r.Context(), body.ClientID)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
		return
	}
	if auth.HashToken(body.ClientSecret) != client.HashedSecret {
		Error(w, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
		return
	}

	token, err := auth.IssueAccessToken(client, s.cfg.JWTSecret)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// GET /api/v1/auth/saml/login — initiates the IdP redirect.
// We clone the request and rewrite the URL to the complete endpoint so that
// crewjam uses /saml/complete as the RelayState, ensuring the browser lands
// there after the ACS round-trip.
func (s *Server) handleSAMLLogin(w http.ResponseWriter, r *http.Request) {
	mw := s.samlHTTP()
	if mw == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL = &url.URL{
		Scheme: func() string {
			if r.TLS != nil {
				return "https"
			}
			return "http"
		}(),
		Host: r.Host,
		Path: "/api/v1/auth/saml/complete",
	}
	mw.ServeHTTP(w, r2)
}

// POST /api/v1/auth/saml/acs — assertion consumer service.
func (s *Server) handleSAMLACS(w http.ResponseWriter, r *http.Request) {
	mw := s.samlHTTP()
	if mw == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	mw.ServeHTTP(w, r)
}

// GET /api/v1/auth/saml/metadata — SP metadata XML for IdP registration.
func (s *Server) handleSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	mw := s.samlHTTP()
	if mw == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	mw.ServeHTTP(w, r)
}

// GET /api/v1/auth/saml/complete — post-ACS landing page.
// crewjam redirects here after a successful assertion. RequireAccount injects
// the SAML session into the context; we then convert it to an app session.
func (s *Server) handleSAMLComplete(w http.ResponseWriter, r *http.Request) {
	mw := s.samlHTTP()
	if mw == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	mw.RequireAccount(http.HandlerFunc(s.handleSAMLSession)).ServeHTTP(w, r)
}

// handleSAMLSession is the inner handler called by RequireAccount once the
// SAML session is validated. It extracts user attributes, upserts the user
// record, and writes the gorilla app session.
func (s *Server) handleSAMLSession(w http.ResponseWriter, r *http.Request) {
	session := samlsp.SessionFromContext(r.Context())
	if session == nil {
		Error(w, http.StatusUnauthorized, "saml_session_missing", "no SAML session")
		return
	}

	claims, ok := session.(samlsp.JWTSessionClaims)
	if !ok {
		Error(w, http.StatusInternalServerError, "saml_session_invalid", "unexpected SAML session type")
		return
	}

	nameID := claims.Subject
	email := firstNonEmpty(
		claims.Attributes.Get("email"),
		claims.Attributes.Get("mail"),
		claims.Attributes.Get("urn:oid:0.9.2342.19200300.100.1.3"),
		nameID, // fall back to NameID when it is an email address
	)
	displayName := firstNonEmpty(
		claims.Attributes.Get("displayName"),
		claims.Attributes.Get("cn"),
		claims.Attributes.Get("name"),
		strings.Join([]string{
			claims.Attributes.Get("givenName"),
			claims.Attributes.Get("sn"),
		}, " "),
		email,
	)

	allowedDomains := s.adminSvc.AllowedEmailDomains(r.Context())
	u, err := s.users.UpsertSAMLUser(r.Context(), nameID, email, displayName, allowedDomains)
	if err != nil {
		if errors.Is(err, user.ErrDomainNotAllowed) {
			http.Redirect(w, r, "/login?error=domain_not_allowed", http.StatusSeeOther)
			return
		}
		handleError(w, err)
		return
	}

	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    u.ID,
		Role:      u.Role,
		MFAPassed: true, // SAML authentication counts as MFA
	}); err != nil {
		handleError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// firstNonEmpty returns the first non-blank string from the arguments.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// writeSession persists session data to the cookie store.
func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, sd auth.SessionData) error {
	// Ignore the decode error: CookieStore.Get always returns a usable session
	// even when an existing cookie can't be decoded (e.g. after a module rename
	// changes gob type paths). We're overwriting the session anyway.
	session, _ := s.sessions.Get(r, auth.SessionName)
	session.Values["session"] = sd
	return session.Save(r, w)
}

// handleAuthProviders returns available authentication providers.
func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	type Provider struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}

	response := struct {
		Providers []Provider `json:"providers"`
	}{
		Providers: []Provider{
			{
				Name:    "local",
				Enabled: true,
			},
			{
				Name: "saml",
				Enabled: s.adminSvc.SAMLEnabled(ctx) &&
					s.adminSvc.SAMLConfigured(ctx),
			},
			{
				Name: "oidc",
				Enabled: func() bool {
					v, _ := s.adminSvc.GetBool(ctx, admin.KeyOIDCEnabled)
					return v
				}(),
			},
		},
	}

	json.NewEncoder(w).Encode(response)
}

// loginRateKey derives a fixed-size rate-limit key from a submitted email.
//
// The key is attacker-supplied and every distinct value is retained for the
// length of the window, so using the address itself made live memory a
// function of inbound bandwidth rather than of account count: 1 MiB emails
// cost 1 MiB of heap each for a minute. Hashing bounds it to 32 bytes per
// distinct value regardless of what is sent.
//
// Normalised exactly as the account lookup normalises, so the same account is
// always the same bucket and no casing or padding variant buys a fresh budget.
func loginRateKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}
