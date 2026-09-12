package auth_test

import (
	"bytes"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/stretchr/testify/require"
)

const goodSecret = "an-adequately-long-session-secret!!"

func TestDeriveSessionKeys(t *testing.T) {
	hashKey, blockKey, err := auth.DeriveSessionKeys(goodSecret)
	require.NoError(t, err)

	// 32 bytes is what selects AES-256 in securecookie; a short key silently
	// picks a weaker cipher.
	require.Len(t, hashKey, 32)
	require.Len(t, blockKey, 32)

	// The whole point of deriving under distinct labels: the encryption key
	// must not be the signing key by another name.
	require.False(t, bytes.Equal(hashKey, blockKey),
		"hmac and encryption keys must be independent")

	// Neither key may be the raw secret handed straight through.
	require.False(t, bytes.Equal(hashKey, []byte(goodSecret)))
	require.False(t, bytes.Equal(blockKey, []byte(goodSecret)))
}

// TestDeriveSessionKeys_IsDeterministic pins the property the whole scheme
// depends on: a restart must derive the same keys, or every session dies with
// the process.
func TestDeriveSessionKeys_IsDeterministic(t *testing.T) {
	h1, b1, err := auth.DeriveSessionKeys(goodSecret)
	require.NoError(t, err)
	h2, b2, err := auth.DeriveSessionKeys(goodSecret)
	require.NoError(t, err)

	require.True(t, bytes.Equal(h1, h2), "derivation must be deterministic across calls")
	require.True(t, bytes.Equal(b1, b2), "derivation must be deterministic across calls")
}

func TestDeriveSessionKeys_DiffersPerSecret(t *testing.T) {
	h1, b1, err := auth.DeriveSessionKeys(goodSecret)
	require.NoError(t, err)
	h2, b2, err := auth.DeriveSessionKeys("a-different-but-equally-long-secret")
	require.NoError(t, err)

	require.False(t, bytes.Equal(h1, h2))
	require.False(t, bytes.Equal(b1, b2))
}

// TestDeriveSessionKeys_RejectsShortSecret guards the one case HKDF cannot fix.
// Expanding a low-entropy secret produces long keys that are no stronger than
// what they came from, so a short secret must be refused outright rather than
// stretched into a false sense of security.
func TestDeriveSessionKeys_RejectsShortSecret(t *testing.T) {
	for _, secret := range []string{"", "short", "still-too-short-at-31-chars-xx"} {
		_, _, err := auth.DeriveSessionKeys(secret)
		require.ErrorIs(t, err, auth.ErrSessionSecretTooShort, "secret %q must be refused", secret)
	}
}
