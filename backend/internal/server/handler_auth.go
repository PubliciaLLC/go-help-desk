package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// count has to gate before the check. It does — see ClaimMFAAttempt.)
	//
	// What the counter could NOT do on its own is limit guessing, and
	// measuring it said so: with a limit of three, eight wrong guesses
	// answered 401 401 401 429 429 429 429 429, and every one of those 429s
	// had still run the password check. Ignore the status code and the
	// guesses were unlimited, bounded only by bcrypt — ten to fifteen a
	// second per core. MFA is off by default, so on most instances that was
	// the whole of the online defence.
	//
	// So an account over its budget waits before the password is checked.
	// Measured, that costs a serial attacker about twenty times and costs one
	// with a couple of hundred parallel connections nothing — a request that
	// cannot get its turn goes ahead rather than being refused. Tarpit has
	// the numbers and the reasoning for that trade. What it keeps is the
	// property this ordering exists for: the right password always works, and
	// nothing here can be used to keep somebody out of their own account.
	loginKey := "login:" + loginRateKey(body.Email)

	if s.loginLimiter.Exceeded(loginKey) {
		s.loginLimiter.Tarpit(r.Context(), loginKey, s.loginThrottleDelay)
	}

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

	// The second-factor gate, after a valid password. There are four answers,
	// and there used to be two.
	//
	//   verify TOTP    — a TOTP enrolment exists
	//   verify passkey — a passkey is registered and no TOTP is
	//   enrol         — neither, and this role must have one
	//   nothing owed  — anything else; the session is fully authenticated
	//
	// The two booleans that used to carry this both keyed off u.MFAEnabled,
	// which is the TOTP column rather than "this account has a second
	// factor". That was a complete answer while TOTP was the only kind.
	// Passkeys made it a wrong one: an account with a passkey and no TOTP
	// read MFAEnabled false, so the server answered "you still need to
	// enrol" on every sign-in and never once offered the key. It could not
	// sign in again. There was no third outcome on the wire to say "you have
	// a factor, use it, and it is not TOTP".
	mfaEnabled := s.adminSvc.MFAEnabled(r.Context())
	// The password was right, so prior failures for this address stop counting.
	s.loginLimiter.Reset(loginKey)

	passkeys, err := s.passkeyStore.CountForUser(r.Context(), u.ID)
	if err != nil {
		handleError(w, err)
		return
	}

	// TOTP first when both exist: it is what the account had before, and a
	// person who enrolled a key alongside it has not asked to stop using the
	// authenticator they already have. The passkey route is still reachable
	// directly for anyone who prefers it.
	mfaNeeded := mfaEnabled && u.MFAEnabled
	passkeyNeeded := mfaEnabled && !u.MFAEnabled && passkeys > 0
	mfaEnrollmentNeeded := mfaEnabled && !u.MFAEnabled && passkeys == 0 &&
		s.adminSvc.MFARequiredFor(r.Context(), string(u.Role))
	mfaPassed := !mfaNeeded && !passkeyNeeded && !mfaEnrollmentNeeded

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
		"passkey_needed":        passkeyNeeded,
		"mfa_enrollment_needed": mfaEnrollmentNeeded,
	})
}

// samlAuthnMethodsAttr and samlMultipleAuthn are how Entra ID says, in a
// SAML assertion, that the user completed MFA: the attribute holds
// multipleauthn only then. For apps other than Salesforce the Entra
// administrator must add the `amr` optional claim with include_granular_amr,
// or the attribute is not sent and SSO sign-ins do not count as a proved
// factor.
const (
	samlAuthnMethodsAttr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
	samlMultipleAuthn    = "http://schemas.microsoft.com/claims/multipleauthn"
)

// samlAssertedMFA reports whether a SAML assertion's attributes say the user
// completed a second factor. Anything else fails closed.
//
// Looked up under both keys the SAML library can file it under: the
// attribute's Name, or its FriendlyName when the provider sets one. Entra
// sends no FriendlyName for this claim; a provider that did would otherwise
// never count as MFA.
func samlAssertedMFA(attrs map[string][]string) bool {
	for _, key := range []string{samlAuthnMethodsAttr, "authnmethodsreferences"} {
		for _, v := range attrs[key] {
			if v == samlMultipleAuthn {
				return true
			}
		}
	}
	return false
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
	//
	// Spent before the code is checked, not counted after it. The old order
	// left a window: forty parallel wrong codes all read "not locked" and
	// thirty-six of them were verified, against a limit of five.
	if err := s.users.ClaimMFAAttempt(r.Context(), a.UserID); err != nil {
		if errors.Is(err, user.ErrMFALocked) {
			tooManyAttempts(w, user.MFALockDuration)
			return
		}
		handleError(w, err)
		return
	}

	if err := s.users.VerifyMFACode(r.Context(), a.UserID, body.Code); err != nil {
		Error(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid TOTP code")
		return
	}
	if err := s.users.ClearMFAFailures(r.Context(), a.UserID); err != nil {
		handleError(w, err)
		return
	}
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:         a.UserID,
		Role:           a.Role,
		MFAPassed:      true,
		FactorVerified: true,
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

// GET /api/v1/auth/saml/login — starts a fresh SAML sign-in at the IdP.
//
// It calls HandleStartAuthFlow directly. mw.ServeHTTP serves only the
// metadata and ACS paths and 404s everything else (#390), and redirecting to
// /saml/complete instead would let a "token" cookie already in the browser
// answer for the IdP: a spent one would send the person to
// /login?error=sso_session_used with no way to start over.
//
// The library records r.URL as where the browser goes after the ACS, so the
// URL is replaced with /saml/complete: left as /saml/login, every successful
// assertion would start another sign-in. It is a bare path on purpose. An
// absolute URL built from the request would take its host from the Host
// header and its scheme from r.TLS, which is nil behind a TLS-terminating
// proxy, so the browser would be sent to http:// and its Secure "token"
// cookie would not go with it. Nothing from the query is read, so this route
// cannot be pointed anywhere else.
func (s *Server) handleSAMLLogin(w http.ResponseWriter, r *http.Request) {
	mw := s.samlHTTP()
	if mw == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL = &url.URL{Path: "/api/v1/auth/saml/complete"}
	mw.HandleStartAuthFlow(w, r2)
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
	mw.RequireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The library's "token" cookie is a signed JWT, valid for an hour,
		// and RequireAccount accepts it in place of a fresh assertion. It is
		// the library's own credential: revoking an app session (password
		// change, MFA reset, a new factor) cannot reach it, so a browser
		// still holding it could come back here and mint a new app session,
		// with whatever MFA the original assertion claimed (#337).
		//
		// So it is spent here, the moment it has been read, and whatever the
		// sign-in's outcome: this route is the only reader, and the app
		// session written below is the credential from now on.
		//
		// Spent twice: the browser is told to delete the cookie (same name,
		// domain and path the library set it with), and the SHA-256 of its signed
		// input (header.payload) is recorded so a copy presented later is
		// refused (SpendSAMLHandover). Both happen before the user is looked
		// up, so a refused sign-in spends it too.
		//
		// The error branches below are defensive. CookieSessionProvider's
		// DeleteSession can only fail on a malformed cookie lookup, which
		// RequireAccount has just ruled out by reading it, so it is not
		// reachable today; if a different SessionProvider is ever configured,
		// failing closed is the safe default.
		if err := mw.Session.DeleteSession(w, r); err != nil {
			handleError(w, fmt.Errorf("clearing SAML login cookie: %w", err))
			return
		}

		// Spent on the server too, not only in this browser: a copy taken
		// before this hand-over is refused from now on (#337). Detached, like
		// the session revocations: once the row is written, a client that
		// hangs up must not turn it into a 500 for a cookie that is already
		// spent.
		cp, ok := mw.Session.(samlsp.CookieSessionProvider)
		if !ok {
			handleError(w, errors.New("SAML session provider is not cookie-based"))
			return
		}
		c, err := r.Cookie(cp.Name)
		if err != nil {
			handleError(w, fmt.Errorf("reading SAML login cookie: %w", err))
			return
		}

		// The spend key is the signed input (header.payload), never the
		// signature segment. The verifier decodes the signature leniently, so
		// one token has several spellings that all verify (the unused low bits
		// of the last base64 character can be changed); each would hash
		// differently, and a spent cookie could be replayed by respelling its
		// signature (#337). header.payload is covered by the signature byte for
		// byte and cannot be respelled without breaking verification. Not three
		// segments cannot happen after RequireAccount; fail closed anyway.
		parts := strings.Split(c.Value, ".")
		if len(parts) != 3 {
			handleError(w, errors.New("invalid SAML cookie format"))
			return
		}
		spendKey := strings.Join(parts[:2], ".")

		first, err := s.sessions.SpendSAMLHandover(context.WithoutCancel(r.Context()), spendKey)
		if err != nil {
			handleError(w, err)
			return
		}
		if !first {
			slog.Warn("spent SAML hand-over cookie presented again")
			http.Redirect(w, r, "/login?error=sso_session_used", http.StatusSeeOther)
			return
		}

		s.handleSAMLSession(w, r)
	})).ServeHTTP(w, r)
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
		// Every refusal the upsert can make, not just the one. Only
		// ErrDomainNotAllowed was mapped, so a disabled user's SAML login —
		// and a provider that sent no subject, or no email — answered 500
		// "an internal error occurred" and was logged as a fault on this
		// server. Nothing is wrong with this server in any of those cases;
		// the login was refused, and the person needs to be told which.
		//
		// The OIDC handler has mapped all of these since it was written.
		// This is the same list, redirected rather than JSON because this
		// endpoint is reached by a browser following the identity provider.
		switch {
		case errors.Is(err, user.ErrDomainNotAllowed):
			http.Redirect(w, r, "/login?error=domain_not_allowed", http.StatusSeeOther)
		case errors.Is(err, user.ErrUserDisabled):
			http.Redirect(w, r, "/login?error=account_disabled", http.StatusSeeOther)
		case errors.Is(err, user.ErrAccountLinkRefused):
			http.Redirect(w, r, "/login?error=account_link_refused", http.StatusSeeOther)
		case errors.Is(err, user.ErrEmailTaken):
			http.Redirect(w, r, "/login?error=email_taken", http.StatusSeeOther)
		case errors.Is(err, user.ErrSubjectRequired):
			http.Redirect(w, r, "/login?error=invalid_assertion", http.StatusSeeOther)
		case errors.Is(err, user.ErrEmailRequired):
			http.Redirect(w, r, "/login?error=email_not_verified", http.StatusSeeOther)
		case errors.Is(err, user.ErrValidation):
			http.Redirect(w, r, "/login?error=invalid_email", http.StatusSeeOther)
		default:
			handleError(w, err)
		}
		return
	}

	if err := s.writeSession(w, r, auth.SessionData{
		UserID:         u.ID,
		Role:           u.Role,
		MFAPassed:      true, // SAML authentication counts as MFA
		FactorVerified: samlAssertedMFA(claims.Attributes),
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

// writeSession stores session data under a NEWLY MINTED session id.
//
// Rotating the id is the whole point, not an implementation detail. The
// callers that matter are privilege changes — login, MFA verification, SAML
// and OIDC callbacks, signup, password change — and reusing the incoming id
// across one of those is session fixation: an attacker obtains a valid id (any
// account will do, including one they signed up for), plants that cookie in a
// victim's
// browser, and when the victim authenticates, the attacker's own cookie is now
// the victim's authenticated session. Verified end to end before this fix: the
// planted cookie returned the victim's identity from /me.
//
// The stateless cookie store this replaced was immune by accident — the cookie
// WAS the state, so logging in overwrote it with a payload the attacker never
// saw. Moving state server-side removes that accident, so the rotation has to
// be deliberate.
//
// OWASP's Session Management guidance puts it as: renew the session identifier
// on any privilege change.
func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, sd auth.SessionData) error {
	// Ignore the decode error: Get always returns a usable session even when an
	// existing cookie can't be decoded. We're replacing it anyway.
	session, _ := s.sessions.Get(r, auth.SessionName)

	if session.ID != "" {
		// Drop the row the incoming cookie pointed at, then clear the id so
		// Save mints a new one. Deleting matters as much as rotating: left
		// behind, the old id keeps working for whoever still holds it.
		if err := s.sessions.Delete(r.Context(), session.ID); err != nil {
			return fmt.Errorf("rotating session: %w", err)
		}
		session.ID = ""
	}

	session.Values[auth.SessionDataKey] = sd
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
