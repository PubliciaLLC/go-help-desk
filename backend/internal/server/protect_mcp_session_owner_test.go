package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// An SSE stream belongs to the credential that opened it, not just the user
// behind it (#495). TestMCPSSE_SessionBoundToActor covers other users; these
// are the two cases that share the user: a second key, and the same person's
// browser session. Both must be refused, or a leaked session id plus any other
// credential of the owner drives the stream under the opener's scopes.
func TestMCPSSE_SessionBoundToCredentialNotJustUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	owner := openMCP(t, h, h.apiKey)
	t.Cleanup(owner.closeBody)

	// A second key for the same staff user.
	rawToken, hashed, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, h.authStore.CreateAPIKey(ctx, auth.APIKey{
		ID:          uuid.New(),
		Name:        "second-key",
		HashedToken: hashed,
		UserID:      h.staffID,
		Scopes:      allScopes(),
		CreatedAt:   time.Now(),
	}))

	// post sends a ping to the owner's stream with whatever credential is given.
	post := func(t *testing.T, mutate func(*http.Request)) *http.Response {
		t.Helper()
		body := []byte(`{"jsonrpc":"2.0","id":9,"method":"ping"}`)
		req, err := http.NewRequest(http.MethodPost, owner.messageURL, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		mutate(req)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	t.Run("the opening key is accepted", func(t *testing.T) {
		resp := post(t, func(r *http.Request) { r.Header.Set("Authorization", "ApiKey "+h.apiKey) })
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
	})
	t.Run("a second key of the same user is refused", func(t *testing.T) {
		resp := post(t, func(r *http.Request) { r.Header.Set("Authorization", "ApiKey "+rawToken) })
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	})
	t.Run("the same user's browser session is refused", func(t *testing.T) {
		s := staffPasswordLogin(t, h)
		resp := post(t, func(r *http.Request) {
			for _, c := range s.jar {
				r.AddCookie(c)
			}
		})
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	})
}
