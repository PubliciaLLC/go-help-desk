package auth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/stretchr/testify/require"
)

// fakeStore is a test implementation of auth.SecretStore
type fakeStore struct {
	secrets map[string][]byte
	err     error
}

func (f *fakeStore) InsertIfAbsent(ctx context.Context, name string, value []byte) error {
	if f.err != nil {
		return f.err
	}
	if _, exists := f.secrets[name]; !exists {
		f.secrets[name] = value
	}
	return nil
}

func (f *fakeStore) Get(ctx context.Context, name string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	val, ok := f.secrets[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return val, nil
}

func TestResolveJWTSecret_EnvWins(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{secrets: make(map[string][]byte)}
	envSecret := "12345678901234567890123456789012" // exactly 32 bytes

	secret, source, err := auth.ResolveJWTSecret(ctx, envSecret, store, nil)
	require.NoError(t, err)
	require.Equal(t, envSecret, secret)
	require.Equal(t, "environment", source)
	require.Empty(t, store.secrets, "store should not be touched when env is 32+ bytes")
}

func TestResolveJWTSecret_GenerateAndStore(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{secrets: make(map[string][]byte)}
	seededRand := bytes.NewReader(bytes.Repeat([]byte{0xAB}, 32))

	secret, source, err := auth.ResolveJWTSecret(ctx, "", store, seededRand)
	require.NoError(t, err)
	require.Equal(t, "database", source)
	require.NotEmpty(t, secret)

	// Verify the secret is base64-encoded
	decoded, err := base64.RawStdEncoding.DecodeString(secret)
	require.NoError(t, err)
	require.Len(t, decoded, 32)

	// Verify it's what we generated
	expected := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 32))
	require.Equal(t, expected, secret)
}

func TestResolveJWTSecret_SecondCallReturnsStored(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{secrets: make(map[string][]byte)}
	seededRand1 := bytes.NewReader(bytes.Repeat([]byte{0xAA}, 32))

	// First call generates and stores
	secret1, source1, err := auth.ResolveJWTSecret(ctx, "", store, seededRand1)
	require.NoError(t, err)
	require.Equal(t, "database", source1)

	// Second call should return the stored value, not generate a new one
	seededRand2 := bytes.NewReader(bytes.Repeat([]byte{0xBB}, 32))
	secret2, source2, err := auth.ResolveJWTSecret(ctx, "", store, seededRand2)
	require.NoError(t, err)
	require.Equal(t, "database", source2)

	// Both should be the same
	require.Equal(t, secret1, secret2, "second call should return the same stored value")
}

func TestResolveJWTSecret_StoreError(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{secrets: make(map[string][]byte), err: errors.New("store error")}
	seededRand := bytes.NewReader(bytes.Repeat([]byte{0xCC}, 32))

	secret, source, err := auth.ResolveJWTSecret(ctx, "", store, seededRand)
	require.Error(t, err)
	require.Empty(t, secret)
	require.Empty(t, source)
	require.Contains(t, err.Error(), "store error")
}

func TestResolveJWTSecret_LoserReadsWinnerValue(t *testing.T) {
	ctx := context.Background()
	// Simulate a loser thread whose insert was rejected
	store := &fakeStore{
		secrets: map[string][]byte{
			"jwt_signing": bytes.Repeat([]byte{0xDD}, 32),
		},
	}
	seededRand := bytes.NewReader(bytes.Repeat([]byte{0xCC}, 32))

	secret, source, err := auth.ResolveJWTSecret(ctx, "", store, seededRand)
	require.NoError(t, err)
	require.Equal(t, "database", source)

	// Even though we tried to insert different bytes, we read back the existing ones
	decoded, _ := base64.RawStdEncoding.DecodeString(secret)
	require.Equal(t, bytes.Repeat([]byte{0xDD}, 32), decoded)
}

func TestResolveJWTSecret_ShortEnv(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{secrets: make(map[string][]byte)}
	seededRand := bytes.NewReader(bytes.Repeat([]byte{0xEE}, 32))

	// 31-byte env secret should trigger generation
	shortEnv := "1234567890123456789012345678901" // exactly 31 bytes
	require.Len(t, shortEnv, 31)

	secret, source, err := auth.ResolveJWTSecret(ctx, shortEnv, store, seededRand)
	require.NoError(t, err)
	require.Equal(t, "database", source)

	// Should not match the env secret
	require.NotEqual(t, shortEnv, secret)
}
