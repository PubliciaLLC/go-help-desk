package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// APIKey is a long-lived bearer token for scripts and webhook senders.
// The raw token is shown once at creation; only the hash is persisted.
type APIKey struct {
	ID         uuid.UUID
	Name       string
	HashedToken string
	UserID     uuid.UUID
	Scopes     []string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	CreatedAt  time.Time
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
	ListByUser(ctx context.Context, userID uuid.UUID) ([]APIKey, error)
}
