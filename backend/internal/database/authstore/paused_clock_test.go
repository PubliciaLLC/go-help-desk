package authstore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #523: authentication and the webhook pause check must call the same key dead
// at the same instant. Authentication asks auth.APIKey.Expired(now); the pause
// check is SQL. This runs both at the instant of expiry and one microsecond
// either side (Postgres stores microseconds), and they must agree at all three.
func TestPausedWebhooks_AgreeWithAuthenticationAboutWhenAKeyIsDead(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	s := New(q)

	owner := uuid.New()
	c := auth.OAuthClient{ID: uuid.New(), ClientID: "c-" + uuid.NewString(), HashedSecret: "h", Name: "owner",
		Scopes: []string{"tickets:read"}, CreatedAt: time.Now().UTC(), UserID: owner}
	require.NoError(t, s.CreateOAuthClient(ctx, c)) // also creates the user row the key needs

	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	key := auth.APIKey{ID: uuid.New(), Name: "k", HashedToken: "h-" + uuid.NewString(), UserID: owner,
		Scopes: []string{"tickets:read"}, ExpiresAt: &expiry, CreatedAt: time.Now().UTC()}
	require.NoError(t, s.CreateAPIKey(ctx, key))

	hook := WebhookConfig{ID: uuid.New(), URL: "https://example.com/h", Events: []string{"ticket.created"},
		Secret: "s", Enabled: true, CreatedAt: time.Now().UTC(), APIKeyID: &key.ID}
	require.NoError(t, s.CreateWebhook(ctx, hook))

	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"a microsecond before", expiry.Add(-time.Microsecond)},
		{"at the instant", expiry},
		{"a microsecond after", expiry.Add(time.Microsecond)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.now = func() time.Time { return tc.now }
			got, err := s.GetWebhook(ctx, hook.ID)
			require.NoError(t, err)
			require.Equal(t, key.Expired(tc.now), got.Paused,
				"the key is dead for authentication exactly when its hook is paused")
		})
	}
}
