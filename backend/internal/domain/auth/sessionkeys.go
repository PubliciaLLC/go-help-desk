package auth

import (
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Session cookies are authenticated with one key and encrypted with another.
// Both are derived from SESSION_SECRET rather than configured separately, so
// operators still manage a single secret.
//
// The labels are what make the two keys independent: HKDF guarantees that
// output derived under different info strings cannot be used to recover the
// other, so the encryption key is not simply the signing key by another name.
// Changing a label rotates that key and invalidates every live session.
const (
	sessionHashKeyLabel  = "ohd session hmac v1"
	sessionBlockKeyLabel = "ohd session encrypt v1"

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
