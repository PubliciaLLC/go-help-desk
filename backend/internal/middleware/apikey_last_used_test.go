package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A key's last_used_at is shown in the UI and the OpenAPI schema, and nothing
// ever wrote it. These pin when APIKeyAuth records a use.

type usedCall struct {
	id     uuid.UUID
	at     time.Time
	ctxErr error
}

// serveWithKey runs one authenticated request through APIKeyAuth and returns
// the calls made to markUsed. cancel hands it a request context that is already
// cancelled, as a client that hung up leaves it.
func serveWithKey(t *testing.T, key auth.APIKey, u user.User, cancel bool) (called chan usedCall, status int) {
	t.Helper()
	raw := "deadbeefdeadbeefdeadbeef12345680"
	key.HashedToken = auth.HashToken(raw)
	called = make(chan usedCall, 4)

	lookup := APIKeyAuthFunc(func(_ context.Context, _ string) (auth.APIKey, user.User, error) {
		return key, u, nil
	})
	mark := APIKeyMarkUsedFunc(func(ctx context.Context, id uuid.UUID, at time.Time) error {
		// Whether the context survived the request is read before the send, so
		// the test sees the state the store would have seen.
		called <- usedCall{id: id, at: at, ctxErr: ctx.Err()}
		return nil
	})

	ctx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	if cancel {
		cancelReq()
	}
	handler := APIKeyAuth(lookup, mark)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetActor(r) == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.Header.Set("Authorization", "ApiKey "+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return called, rr.Code
}

func TestAPIKeyAuth_RecordsLastUsed(t *testing.T) {
	u := user.User{ID: uuid.New(), Role: user.RoleStaff}
	hourAgo := time.Now().Add(-time.Hour)
	justNow := time.Now().Add(-time.Second)

	t.Run("a key never used is recorded", func(t *testing.T) {
		key := auth.APIKey{ID: uuid.New(), UserID: u.ID}
		called, status := serveWithKey(t, key, u, false)
		require.Equal(t, http.StatusNoContent, status)
		select {
		case c := <-called:
			require.Equal(t, key.ID, c.id)
			require.WithinDuration(t, time.Now(), c.at, 5*time.Second)
		case <-time.After(2 * time.Second):
			t.Fatal("last_used_at was not recorded")
		}
	})

	t.Run("a key last recorded long ago is recorded again", func(t *testing.T) {
		key := auth.APIKey{ID: uuid.New(), UserID: u.ID, LastUsedAt: &hourAgo}
		called, _ := serveWithKey(t, key, u, false)
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("last_used_at was not refreshed")
		}
	})

	t.Run("a key recorded a moment ago is not written again", func(t *testing.T) {
		key := auth.APIKey{ID: uuid.New(), UserID: u.ID, LastUsedAt: &justNow}
		called, status := serveWithKey(t, key, u, false)
		require.Equal(t, http.StatusNoContent, status)
		select {
		case <-called:
			t.Fatal("a busy key wrote the database on every request")
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("the write outlives a client that hangs up", func(t *testing.T) {
		key := auth.APIKey{ID: uuid.New(), UserID: u.ID}
		called, _ := serveWithKey(t, key, u, true)
		select {
		case c := <-called:
			require.NoError(t, c.ctxErr, "the write was cancelled with the request")
		case <-time.After(2 * time.Second):
			t.Fatal("last_used_at was not recorded")
		}
	})

	t.Run("a refused key is not recorded as used", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		expired := auth.APIKey{ID: uuid.New(), UserID: u.ID, ExpiresAt: &past}
		called, status := serveWithKey(t, expired, u, false)
		require.Equal(t, http.StatusUnauthorized, status)

		disabled := user.User{ID: u.ID, Role: user.RoleStaff, Disabled: true}
		called2, status := serveWithKey(t, auth.APIKey{ID: uuid.New(), UserID: u.ID}, disabled, false)
		require.Equal(t, http.StatusUnauthorized, status)

		select {
		case <-called:
			t.Fatal("an expired key was recorded as used")
		case <-called2:
			t.Fatal("a disabled user's key was recorded as used")
		case <-time.After(200 * time.Millisecond):
		}
	})
}
