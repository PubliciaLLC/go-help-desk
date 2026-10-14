package auth

import (
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// SessionData is the payload stored in the signed session cookie.
// MFAPassed is false until the user completes the TOTP challenge; requests
// to MFA-protected routes are rejected until it is true.
type SessionData struct {
	UserID    uuid.UUID
	Role      user.Role
	MFAPassed bool

	// FactorVerified says this session proved a second factor: a TOTP code,
	// a passkey, a factor it just enrolled, or an identity provider that
	// asserted MFA. MFAPassed cannot answer that — it is also true when the
	// login owed no second factor at all (MFA off, or optional for the role),
	// and a session like that must not be able to replace a factor the
	// account has since gained (#333). Only adding or removing a factor reads
	// it; every other route still gates on MFAPassed. Zero for sessions
	// written before it existed, which fails closed: sign in again to change
	// factors.
	FactorVerified bool

	// MustChangePassword mirrors users.must_change_password as of sign-in
	// (#497). While it is set the session may change the password and do
	// nothing else; see middleware.RequireMFA.
	MustChangePassword bool

	// OIDCState stores the temporary OAuth2 state value used during login.
	// It is cleared after callback validation.
	OIDCState string

	// PendingMFASecret holds a TOTP secret that has been shown to the user but
	// not yet confirmed with a code from it.
	//
	// It lives here rather than on the user row because writing it to the row
	// destroyed the authenticator the user was still relying on: starting
	// enrolment overwrote MFASecret while MFAEnabled stayed true, so merely
	// opening the rotate-authenticator screen and closing it locked the user
	// out, and an administrator reset was the only way back — for a sole
	// administrator, no way back at all.
	PendingMFASecret string

	// PendingPasskey holds a staged WebAuthn challenge as opaque JSON, for the
	// same reason PendingMFASecret is here: nothing is written to the account
	// until the person has proved they hold the key.
	//
	// Opaque rather than typed, so this package does not take on the WebAuthn
	// library as a dependency. internal/domain/auth is imported by the session
	// store, the middleware and every handler; a credential format does not
	// belong in that blast radius, and the only thing done with this value is
	// handing it back to the package that minted it.
	//
	// It carries its own expiry, checked when the ceremony finishes. A
	// challenge left in a long-lived session is a replay window that stays
	// open as long as the tab does.
	PendingPasskey string

	// OIDCNonce is the nonce sent with the authorization request. The callback
	// requires the ID token to echo it back.
	OIDCNonce string

	// OIDCCodeVerifier is the PKCE code_verifier whose challenge was sent with
	// the authorization request. It is presented at token exchange.
	OIDCCodeVerifier string

	// PendingUntil is the moment a half-signed-in session stops being usable.
	// It is a deadline rather than a start time so the limit is decided once,
	// where the session is born (PendingDeadline), and nothing downstream has to
	// know which kind of pending session it is looking at.
	//
	// Not refreshed on later saves. Zero when MFAPassed is true or the session
	// is pre-login (UserID is nil); zero on a pending session means it was
	// saved before this field existed, and PendingExpired treats that as expired.
	PendingUntil time.Time
}

// SessionName is the session cookie's name.
//
// It was ohd_session until the rename to Go Help Desk. Renaming a cookie logs
// everyone out, because the browser keeps sending the old name and the server
// no longer reads it — so it was done in the same release as the HKDF label
// rename (see sessionkeys.go), which invalidates every session anyway. One
// logout, not two.
const SessionName = "ghd_session"

// SessionDataKey is the key SessionData is stored under inside a session.
//
// Named because the session store reads it too: it lifts the user id out of
// the payload into an indexed column so every session a user holds can be
// revoked without decoding each row. A bare string literal in two packages is
// how those two quietly stop agreeing.
const SessionDataKey = "session"

// LegacySessionName is the pre-rename cookie name. Nothing reads it: a cookie
// under this name cannot be decrypted any more regardless, because the keys
// that signed it were derived under the old HKDF labels.
//
// It exists so the old cookie can be actively expired rather than left in the
// browser for its full 30-day MaxAge, sent on every request and readable by
// nothing. ExpireLegacySession only ever deletes — it never reads or trusts the
// value — which is what makes carrying a legacy name here safe.
const LegacySessionName = "ohd_session"

const (
	// MFACodeWindow is how long a person has to enter a code from a factor
	// they already have, after the password step.
	MFACodeWindow = 5 * time.Minute
	// MFAEnrolWindow is longer because first-time enrolment means installing
	// an app and scanning a code.
	MFAEnrolWindow = 15 * time.Minute
)

// PendingDeadline is the PendingUntil for a session created now that has
// passed the password but owes a second factor. enrolment is true when the
// factor is yet to be set up rather than entered.
func PendingDeadline(now time.Time, enrolment bool) time.Time {
	if enrolment {
		return now.Add(MFAEnrolWindow)
	}
	return now.Add(MFACodeWindow)
}

// PendingExpired reports whether this is a half-signed-in session (password
// passed, MFA not) whose deadline has passed. A pending session with no
// deadline was saved before PendingUntil existed; with no way to tell how old
// it is, it counts as expired, and its holder signs in again.
func (s SessionData) PendingExpired(now time.Time) bool {
	if s.MFAPassed || s.UserID == uuid.Nil {
		return false
	}
	return s.PendingUntil.IsZero() || now.After(s.PendingUntil)
}
