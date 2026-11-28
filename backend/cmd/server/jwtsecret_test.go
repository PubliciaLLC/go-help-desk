package main

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

type memSecrets map[string][]byte

func (m memSecrets) InsertIfAbsent(_ context.Context, name string, v []byte) error {
	if _, ok := m[name]; !ok {
		m[name] = v
	}
	return nil
}

func (m memSecrets) Get(_ context.Context, name string) ([]byte, error) {
	v, ok := m[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

// An example JWT_SECRET published in the repository is discarded, not used:
// anyone can read it, so a token signed with it proves nothing. The generated
// key stands in, exactly as for an unset or short value.
func TestResolveJWTSecret_ExampleValueNeverSigns(t *testing.T) {
	examples := []string{
		"change-me-in-production-this-is-really-insecure", // 48 bytes, long enough to win otherwise
		"dev-session-secret-change-me-32c",                // 32 bytes
		"CHANGE-ME-IN-PRODUCTION-THIS-IS-REALLY-INSECURE", // case does not rescue it
	}
	for _, example := range examples {
		t.Run(example, func(t *testing.T) {
			secret, source, discarded, err := resolveJWTSecret(context.Background(), example, memSecrets{}, rand.Reader)
			require.NoError(t, err)
			require.True(t, discarded, "the caller logs once when this is true")
			require.Equal(t, "database", source)
			require.NotEqual(t, example, secret)

			// The property that matters: a token forged with the published
			// value does not verify against the key actually in use.
			forged, err := auth.IssueAccessToken(auth.OAuthClient{ID: uuid.New(), ClientID: "x", Scopes: []string{"tickets:read"}}, example)
			require.NoError(t, err)
			_, err = auth.VerifyAccessToken(forged, secret)
			require.Error(t, err)
		})
	}
}

func TestResolveJWTSecret_RealLongValueStillWins(t *testing.T) {
	real := "h8Kq2vZ9mNpR4tXwY6bC1dF3gJ5kL7nQ0sU2wA4eG6i="
	secret, source, discarded, err := resolveJWTSecret(context.Background(), real, memSecrets{}, rand.Reader)
	require.NoError(t, err)
	require.False(t, discarded)
	require.Equal(t, "environment", source)
	require.Equal(t, real, secret)
}

func TestResolveJWTSecret_ExampleAndStoredKeyAgreeAcrossRestarts(t *testing.T) {
	store := memSecrets{}
	a, _, _, err := resolveJWTSecret(context.Background(), "change-me-in-production", store, rand.Reader)
	require.NoError(t, err)
	b, _, _, err := resolveJWTSecret(context.Background(), "", store, rand.Reader)
	require.NoError(t, err)
	require.Equal(t, a, b)
}
