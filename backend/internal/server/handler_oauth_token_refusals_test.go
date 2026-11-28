package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// POST /auth/oauth/token hands out a bearer token for a client id and secret.
// Only the success path had a test; these are the refusals.
func TestOAuthToken_Refusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "token-refusals", "tickets:read")

	post := func(body map[string]any) (int, string, string) {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/oauth/token", body)
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		return res.StatusCode, env.Error.Code, string(raw)
	}
	grant := func(clientID, secret string) map[string]any {
		return map[string]any{"grant_type": "client_credentials", "client_id": clientID, "client_secret": secret}
	}

	t.Run("the right secret is accepted", func(t *testing.T) {
		status, _, body := post(grant(c.ClientID, c.Secret))
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "access_token")
	})

	var wrongSecretBody string
	t.Run("a wrong secret is refused", func(t *testing.T) {
		status, code, body := post(grant(c.ClientID, c.Secret+"x"))
		require.Equal(t, http.StatusUnauthorized, status)
		require.Equal(t, "invalid_client", code)
		require.NotContains(t, body, "access_token")
		wrongSecretBody = body
	})
	t.Run("an empty secret is refused", func(t *testing.T) {
		status, code, _ := post(grant(c.ClientID, ""))
		require.Equal(t, http.StatusUnauthorized, status)
		require.Equal(t, "invalid_client", code)
	})
	t.Run("an unknown client is refused with the same answer as a wrong secret", func(t *testing.T) {
		status, code, body := post(grant("no-such-client", c.Secret))
		require.Equal(t, http.StatusUnauthorized, status)
		require.Equal(t, "invalid_client", code)
		require.Equal(t, wrongSecretBody, body, "the answer must not say which half was wrong")
	})
	t.Run("another grant type is refused", func(t *testing.T) {
		for _, gt := range []string{"password", "authorization_code", "refresh_token", ""} {
			status, code, body := post(map[string]any{
				"grant_type": gt, "client_id": c.ClientID, "client_secret": c.Secret,
			})
			require.Equal(t, http.StatusBadRequest, status, "grant_type %q: %s", gt, body)
			require.Equal(t, "unsupported_grant_type", code)
			require.NotContains(t, body, "access_token")
		}
	})
}
