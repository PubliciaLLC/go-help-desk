package auth

import (
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

	// OIDCNonce is the nonce sent with the authorization request. The callback
	// requires the ID token to echo it back.
	OIDCNonce string

	// OIDCCodeVerifier is the PKCE code_verifier whose challenge was sent with
	// the authorization request. It is presented at token exchange.
	OIDCCodeVerifier string
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
