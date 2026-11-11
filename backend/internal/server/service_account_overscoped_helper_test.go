package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// newOverScopedServiceClient is newServiceClient for a client that holds scopes
// its service user's role cannot use. The admin API refuses to make one (creation
// checks the scopes against the role), but one still exists: it was made before
// that check, or the user's role was lowered after, and every request it makes
// must still be stopped by the role check at request time. The row goes straight
// into the store, so that is the layer these tests exercise.
func (h *harness) newOverScopedServiceClient(t *testing.T, name string, scopes ...string) serviceClient {
	t.Helper()
	raw, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	clientIDRaw, _, err := auth.GenerateToken()
	require.NoError(t, err)
	c := auth.OAuthClient{
		ID:           uuid.New(),
		ClientID:     clientIDRaw[:16],
		HashedSecret: hashed,
		Name:         name,
		Scopes:       scopes,
		CreatedAt:    time.Now(),
		UserID:       uuid.New(), // its service user, created in the same statement
	}
	require.NoError(t, h.authStore.CreateOAuthClient(context.Background(), c))
	return serviceClient{
		ID: c.ID, UserID: c.UserID, ClientID: c.ClientID, Secret: raw,
		Token: h.oauthToken(t, c.ClientID, raw),
	}
}
