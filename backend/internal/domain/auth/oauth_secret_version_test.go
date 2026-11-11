package auth_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// A token names the secret it was issued under, so rotating the secret can
// refuse every token issued before it (#622).
func TestAccessToken_CarriesTheSecretVersion(t *testing.T) {
	_, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	c := auth.OAuthClient{ID: uuid.New(), ClientID: "c1", HashedSecret: hashed, Scopes: []string{"tickets:read"}}

	tok, err := auth.IssueAccessToken(c, "jwt-secret")
	require.NoError(t, err)
	claims, err := auth.VerifyAccessToken(tok, "jwt-secret")
	require.NoError(t, err)
	require.Equal(t, hashed[:16], claims.SecretVersion)
	require.Equal(t, auth.SecretVersion(c), claims.SecretVersion)
}

func TestSecretVersion_ChangesWithTheSecretAndIsOnlyAPrefix(t *testing.T) {
	_, h1, _ := auth.GenerateToken()
	_, h2, _ := auth.GenerateToken()
	v1 := auth.SecretVersion(auth.OAuthClient{HashedSecret: h1})
	v2 := auth.SecretVersion(auth.OAuthClient{HashedSecret: h2})
	require.NotEqual(t, v1, v2)
	require.Len(t, v1, 16, "a prefix only: the token is readable by whoever holds it")
	require.Less(t, len(v1), len(h1))
}

func TestSecretVersion_ShortOrEmptyHashIsEmpty(t *testing.T) {
	require.Empty(t, auth.SecretVersion(auth.OAuthClient{}))
	require.Empty(t, auth.SecretVersion(auth.OAuthClient{HashedSecret: "abc"}))
}
