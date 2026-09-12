package auth_test

import (
	"bytes"
	"encoding/hex"
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

func TestSecureCookies(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "https enables Secure", baseURL: "https://help.example.com", want: true},
		{name: "https is case-insensitive", baseURL: "HTTPS://help.example.com", want: true},
		{name: "surrounding whitespace is ignored", baseURL: "  https://help.example.com  ", want: true},
		{name: "plain http does not", baseURL: "http://localhost:8080", want: false},
		{name: "empty does not", baseURL: "", want: false},

		// A Secure cookie is dropped by the browser over plain HTTP, so getting
		// this wrong on localhost makes login silently do nothing.
		{name: "http host merely containing https does not", baseURL: "http://https.example.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, auth.SecureCookies(tc.baseURL))
		})
	}
}

// TestDeriveSessionKeys_GoldenValues pins the exact bytes derived from a known
// secret.
//
// The HKDF labels are part of the key, so editing one — or the hash, or the key
// length — silently rotates the session keys and logs out every user of every
// deployed instance. That is a migration decision, never a refactor, and it
// must not be possible to make it by accident. If this test fails, the change
// under it ends every live session: either revert it, or ship it deliberately
// as a release that announces the forced re-login.
//
// The rename from "ohd session …" to "ghd session …" was exactly such a
// deliberate change, and these values are from after it.
func TestDeriveSessionKeys_GoldenValues(t *testing.T) {
	const (
		wantHash  = "0d4bf428ac89efcf4cbeee0d9c33f74df9993e211747bf028b39a7fce330cbb6"
		wantBlock = "d4dc2a6a84185fc9a9fdc893b99812ad331433640e551399dd6da9669ee82133"
	)

	hashKey, blockKey, err := auth.DeriveSessionKeys(goodSecret)
	require.NoError(t, err)

	require.Equal(t, wantHash, hex.EncodeToString(hashKey),
		"session hmac key changed — this logs out every user; see the doc comment")
	require.Equal(t, wantBlock, hex.EncodeToString(blockKey),
		"session encryption key changed — this logs out every user; see the doc comment")
}

// The cookie name is pinned for the same reason as the keys: renaming it logs
// everyone out, because the browser keeps sending the old name.
func TestSessionName(t *testing.T) {
	require.Equal(t, "ghd_session", auth.SessionName,
		"renaming the session cookie logs out every user; do it deliberately")
	require.Equal(t, "ohd_session", auth.LegacySessionName,
		"the legacy name must stay accurate — it is what gets expired")
	require.NotEqual(t, auth.SessionName, auth.LegacySessionName)
}
