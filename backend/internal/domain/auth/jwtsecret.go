package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
)

// SecretStore is the interface for storing and retrieving instance secrets.
type SecretStore interface {
	InsertIfAbsent(ctx context.Context, name string, value []byte) error
	Get(ctx context.Context, name string) ([]byte, error)
}

// ResolveJWTSecret resolves the JWT signing key from the environment or database.
// If env is 32+ bytes, it returns env as-is with source "environment".
// Otherwise, it generates a random 32-byte key, stores it in the database
// (insert-if-absent for race-free creation), retrieves it back, and returns it
// as a base64-encoded string with source "database".
//
// rnd is used for generating random bytes (typically rand.Reader in production,
// a seeded reader in tests).
func ResolveJWTSecret(ctx context.Context, env string, store SecretStore, rnd io.Reader) (secret string, source string, err error) {
	// If env is 32+ bytes, use it as-is
	if len(env) >= 32 {
		return env, "environment", nil
	}

	// Generate a random 32-byte key
	key := make([]byte, 32)
	if _, err := io.ReadFull(rnd, key); err != nil {
		return "", "", fmt.Errorf("generating random key: %w", err)
	}

	// Insert into database (race-free: whoever's insert lands first wins)
	if err := store.InsertIfAbsent(ctx, "jwt_signing", key); err != nil {
		return "", "", fmt.Errorf("inserting jwt signing key: %w", err)
	}

	// Retrieve the stored value (either our insert or someone else's)
	storedKey, err := store.Get(ctx, "jwt_signing")
	if err != nil {
		return "", "", fmt.Errorf("retrieving jwt signing key: %w", err)
	}

	// Return as base64-encoded string
	return base64.RawStdEncoding.EncodeToString(storedKey), "database", nil
}
