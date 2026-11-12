package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// OAuthClient represents a machine-to-machine integration using the client
// credentials grant. The secret is hashed on write; the raw secret is shown
// once at creation.
type OAuthClient struct {
	ID           uuid.UUID `json:"id"`
	ClientID     string    `json:"client_id"`
	HashedSecret string    `json:"-"` // never leaves the server; handleListOAuthClients serves this struct directly
	Name         string    `json:"name"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"created_at"`
	// UserID is the client's service user: the identity it acts as. One per
	// client, made with it, never shared and never a person's account.
	UserID uuid.UUID `json:"user_id"`
}

// ClientGroup is a group an OAuth client's service user belongs to: what the
// admin page shows beside the client, because those groups are the client's
// ticket scope.
type ClientGroup struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Claims is the JWT payload issued for client credentials tokens.
type Claims struct {
	ClientID string   `json:"cid"`
	Scopes   []string `json:"scopes"`
	// SecretVersion names the client secret the token was issued under, so
	// rotating the secret (#622) revokes every token issued before it. Empty on
	// a token issued before the claim existed, which stays valid until it
	// expires; refusing those would log every integration out on upgrade.
	SecretVersion string `json:"sv,omitempty"`
	jwt.RegisteredClaims
}

// SecretVersion identifies the client's current secret without a column for it:
// a short prefix of the stored hash, which changes whenever the secret does. It
// is a prefix on purpose, since the token carrying it is readable by whoever
// holds it and the full hash must not be. A hash too short to be real yields
// "", which is the "no version" a legacy token has, so it is never compared.
func SecretVersion(c OAuthClient) string {
	const n = 16
	if len(c.HashedSecret) < n {
		return ""
	}
	return c.HashedSecret[:n]
}

const accessTokenTTL = time.Hour

// IssueAccessToken signs a short-lived JWT for the given client.
func IssueAccessToken(client OAuthClient, jwtSecret string) (string, error) {
	now := time.Now()
	claims := Claims{
		ClientID:      client.ClientID,
		Scopes:        client.Scopes,
		SecretVersion: SecretVersion(client),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   client.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(jwtSecret))
	if err != nil {
		return "", fmt.Errorf("signing access token: %w", err)
	}
	return signed, nil
}

// VerifyAccessToken parses and validates a JWT, returning the embedded claims.
func VerifyAccessToken(tokenString, jwtSecret string) (Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return Claims{}, fmt.Errorf("parsing token: %w", err)
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return Claims{}, fmt.Errorf("invalid token claims")
	}
	return *claims, nil
}

// OAuthClientStore is the persistence interface for OAuth clients.
type OAuthClientStore interface {
	Create(ctx context.Context, c OAuthClient) error
	GetByClientID(ctx context.Context, clientID string) (OAuthClient, error)
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]OAuthClient, error)
}
