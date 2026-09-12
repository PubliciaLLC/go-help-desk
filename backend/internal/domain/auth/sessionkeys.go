package auth

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// Session cookies are authenticated with one key and encrypted with another.
// Both are derived from SESSION_SECRET rather than configured separately, so
// operators still manage a single secret.
//
// The labels are what make the two keys independent: HKDF guarantees that
// output derived under different info strings cannot be used to recover the
// other, so the encryption key is not simply the signing key by another name.
//
// A label is part of the key. Changing one rotates that key and logs out every
// live session — which is why TestDeriveSessionKeys_GoldenValues pins the
// derived bytes: an accidental edit here must fail a test, not quietly end
// everyone's session. The rename from "ohd" was a deliberate, one-time
// instance of exactly that cost.
const (
	sessionHashKeyLabel  = "ghd session hmac v1"
	sessionBlockKeyLabel = "ghd session encrypt v1"

	// 32 bytes selects AES-256 in gorilla/securecookie.
	sessionKeyLen = 32
)

// ErrSessionSecretTooShort reports a SESSION_SECRET with too little entropy to
// derive from. HKDF expands a short secret without adding strength, so a weak
// secret must be refused rather than silently stretched.
var ErrSessionSecretTooShort = errors.New("SESSION_SECRET must be at least 32 characters")

// DeriveSessionKeys returns the HMAC and encryption keys for the session
// cookie store, derived from the configured session secret.
func DeriveSessionKeys(secret string) (hashKey, blockKey []byte, err error) {

	if len(secret) < sessionKeyLen {
		return nil, nil, ErrSessionSecretTooShort
	}

	// No salt: the secret is already high-entropy (the documented way to
	// generate it is `openssl rand -base64 32`), and a per-cookie salt would
	// add nothing over the random IV securecookie already applies per message.
	hashKey, err = hkdf.Key(sha256.New, []byte(secret), nil, sessionHashKeyLabel, sessionKeyLen)
	if err != nil {
		return nil, nil, fmt.Errorf("deriving session hmac key: %w", err)
	}

	blockKey, err = hkdf.Key(sha256.New, []byte(secret), nil, sessionBlockKeyLabel, sessionKeyLen)
	if err != nil {
		return nil, nil, fmt.Errorf("deriving session encryption key: %w", err)
	}

	return hashKey, blockKey, nil
}

// SecureCookies reports whether the session cookie should carry the Secure
// attribute, derived from the configured base URL.
//
// It is derived rather than configured because either constant is wrong: always
// true breaks local development on http://localhost, where the browser silently
// drops the cookie and login appears to do nothing; always false ships a session
// cookie that travels in the clear on any downgrade to HTTP behind TLS.
func SecureCookies(baseURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(baseURL)), "https://")
}
