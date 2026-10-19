package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// APIKey is a long-lived bearer token for scripts and webhook senders.
// The raw token is shown once at creation; only the hash is persisted.
type APIKey struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	HashedToken string     `json:"-"`
	UserID      uuid.UUID  `json:"user_id"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	// CreatedBy is who issued the key (#450). Equal to UserID when the owner
	// made it; different when an administrator issued it on their behalf; nil
	// when it is not known (made before this was recorded, or the issuer has
	// since been deleted).
	CreatedBy *uuid.UUID `json:"created_by"`
}

// Expired reports whether the key is dead at now: from the instant of its
// expiry on, not after it, so the stored instant itself is already too late
// (the same strictness as sessions and guest links, expires_at > now). A key
// with no expiry never is. This is the one rule for "is this key dead": the request
// check and the webhook pause check both use the application's clock through
// it, so they cannot disagree about a key (#523).
func (k APIKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// APIKeyWithOwner is a key with the account it belongs to, for the admin list.
// The embedded key flattens into the same JSON object.
type APIKeyWithOwner struct {
	APIKey
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

// HashToken returns the SHA-256 hex digest of a raw token.
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// GenerateToken produces a cryptographically random 32-byte token and its hash.
func GenerateToken() (raw, hashed string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating token: %w", err)
	}
	raw = hex.EncodeToString(b)
	hashed = HashToken(raw)
	return raw, hashed, nil
}

// APIKeyStore is the persistence interface for API keys.
type APIKeyStore interface {
	Create(ctx context.Context, k APIKey) error
	GetByHash(ctx context.Context, hashed string) (APIKey, error)
	UpdateLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error
	Delete(ctx context.Context, id uuid.UUID) error
	// DeleteForUser deletes the key only if userID owns it; ErrNotFound
	// otherwise, whether it is someone else's or does not exist.
	DeleteForUser(ctx context.Context, id, userID uuid.UUID) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]APIKey, error)
}

var (
	// ErrNotFound is returned when an API key, OAuth client, or webhook is not found.
	ErrNotFound = errors.New("not found")
)
