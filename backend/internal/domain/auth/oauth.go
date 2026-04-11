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
	ID           uuid.UUID
	ClientID     string
	HashedSecret string
	Name         string
	Scopes       []string
	CreatedAt    time.Time
}

// Claims is the JWT payload issued for client credentials tokens.
type Claims struct {
	ClientID string   `json:"cid"`
	Scopes   []string `json:"scopes"`
	jwt.RegisteredClaims
}

const accessTokenTTL = time.Hour

// IssueAccessToken signs a short-lived JWT for the given client.
func IssueAccessToken(client OAuthClient, jwtSecret string) (string, error) {
	now := time.Now()
	claims := Claims{
		ClientID: client.ClientID,
		Scopes:   client.Scopes,
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
