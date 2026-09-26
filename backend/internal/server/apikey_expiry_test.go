package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// requestWithKey sends a GET authenticated with a raw API key token.
func requestWithKey(t *testing.T, h *harness, token, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "ApiKey "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// An expiry asked for at creation is an expiry the key actually has.
//
// The field was decoded off the request and then never copied onto the key,
// so every key lived forever. The middleware honours ExpiresAt, the API
// client type declares it, and the admin page has an "Expires" column that
// could therefore never be filled — everything downstream was ready and the
// one assignment was missing. A key created with a date in 2020 worked.
//
// The people this bites are the ones being careful: somebody putting a
// deliberate expiry on a script's credential got a credential with none, and
// nothing said so.
func TestCreateAPIKey_HonoursTheExpiryItWasGiven(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("a key that has already expired is refused on every request", func(t *testing.T) {
		// Created through the store with a past expiry rather than through
		// the API, which now refuses one — the point here is the middleware,
		// not the handler.
		raw, _, err := auth.GenerateToken()
		require.NoError(t, err)
		raw = "GHD_" + raw
		past := time.Now().Add(-time.Hour)
		require.NoError(t, h.authStore.CreateAPIKey(t.Context(), auth.APIKey{
			ID: uuid.New(), Name: "expired", HashedToken: auth.HashToken(raw),
			UserID: h.adminID, Scopes: []string{"tickets:read"},
			ExpiresAt: &past, CreatedAt: time.Now().Add(-2 * time.Hour),
		}))

		res := requestWithKey(t, h, raw, "/api/v1/tickets")
		res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("an expiry in the past is refused at creation", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
			"name": "already dead", "scopes": []string{"tickets:read"},
			"expires_at": "2020-01-01T00:00:00Z",
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"a credential that expires before it is issued is not a request anybody means")
	})

	t.Run("an unreadable expiry is refused rather than ignored", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
			"name": "typo", "scopes": []string{"tickets:read"},
			"expires_at": "next tuesday",
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"ignoring it is how a key silently outlives the date somebody chose for it")
	})

	t.Run("a future expiry is recorded and returned", func(t *testing.T) {
		want := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
			"name": "short lived", "scopes": []string{"tickets:read"},
			"expires_at": want.Format(time.RFC3339),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)

		var created struct {
			ID        string     `json:"id"`
			Token     string     `json:"token"`
			ExpiresAt *time.Time `json:"expires_at"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
		require.NotNil(t, created.ExpiresAt,
			"the response says the key has no expiry, which is what the bug looked like from outside")
		require.WithinDuration(t, want, created.ExpiresAt.UTC(), time.Second)

		// And it is on the row, not only in the reply.
		listed := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/api-keys", nil)
		defer listed.Body.Close()
		var keys []struct {
			ID        string     `json:"id"`
			ExpiresAt *time.Time `json:"expires_at"`
		}
		require.NoError(t, json.NewDecoder(listed.Body).Decode(&keys))
		var found bool
		for _, k := range keys {
			if k.ID == created.ID {
				found = true
				require.NotNil(t, k.ExpiresAt, "the stored key has no expiry")
				require.WithinDuration(t, want, k.ExpiresAt.UTC(), time.Second)
			}
		}
		require.True(t, found, "the key is not in the list")

		// It still works today, which is the other half: an expiry must not
		// break a key before its date.
		use := requestWithKey(t, h, created.Token, "/api/v1/tickets")
		use.Body.Close()
		require.Equal(t, http.StatusOK, use.StatusCode)
	})
}
