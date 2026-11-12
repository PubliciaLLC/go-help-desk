package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// POST /admin/oauth-clients/{id}/rotate-secret (#622, owner decisions D15 and
// D16): the secret is replaced at once with no overlap, tokens issued under the
// old one stop working, and tokens that name no secret version (issued before
// the upgrade) are still accepted.

type rotatedClient struct {
	ID       uuid.UUID
	ClientID string
	Secret   string
}

func (h *harness) makeRotatableClient(t *testing.T) rotatedClient {
	t.Helper()
	res := h.createCredential(t, clientsPath, map[string]any{"name": "Nightly sync", "scopes": []string{"tickets:read"}})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var out struct {
		ID           uuid.UUID `json:"id"`
		ClientID     string    `json:"client_id"`
		ClientSecret string    `json:"client_secret"`
	}
	require.NoError(t, json.Unmarshal(b, &out))
	return rotatedClient{out.ID, out.ClientID, out.ClientSecret}
}

func rotatePath(id uuid.UUID) string { return clientsPath + "/" + id.String() + "/rotate-secret" }

// tokenStatus is what a bearer token gets on a route a tickets:read client may call.
func (h *harness) tokenStatus(t *testing.T, token string) int {
	t.Helper()
	res := h.doWithBearer(t, token, "/api/v1/tickets")
	res.Body.Close()
	return res.StatusCode
}

func (h *harness) tokenGrantStatus(t *testing.T, clientID, secret string) int {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(map[string]any{
		"grant_type": "client_credentials", "client_id": clientID, "client_secret": secret,
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/token", &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Code
}

func TestRotateOAuthClientSecret_ReplacesTheSecretAndRevokesTokens(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.makeRotatableClient(t)
	oldToken := h.oauthToken(t, c.ClientID, c.Secret)
	require.Equal(t, http.StatusOK, h.tokenStatus(t, oldToken), "baseline: the token works before rotation")

	// A token as 1.3.0-beta issued it: valid, signed, and naming no secret version.
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		ClientID: c.ClientID, Scopes: []string{"tickets:read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: c.ID.String(), IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString([]byte("test-jwt-secret"))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, h.tokenStatus(t, legacy))

	res := h.createCredential(t, rotatePath(c.ID), nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, c.ID.String(), out["id"])
	require.Equal(t, c.ClientID, out["client_id"], "the client id does not change")
	newSecret, _ := out["client_secret"].(string)
	require.NotEmpty(t, newSecret)
	require.NotEqual(t, c.Secret, newSecret)

	require.Equal(t, http.StatusUnauthorized, h.tokenGrantStatus(t, c.ClientID, c.Secret), "D15: the old secret stops at once, no grace period")
	require.Equal(t, http.StatusOK, h.tokenGrantStatus(t, c.ClientID, newSecret))

	require.Equal(t, http.StatusUnauthorized, h.tokenStatus(t, oldToken), "D16: a token issued under the old secret is revoked")
	require.Equal(t, http.StatusOK, h.tokenStatus(t, legacy), "D16: a token with no version is still accepted")
	require.Equal(t, http.StatusOK, h.tokenStatus(t, h.oauthToken(t, c.ClientID, newSecret)), "a token under the new secret works")
}

// A brand new client's tokens carry a version and must not be refused by it.
func TestRotateOAuthClientSecret_TokensOfANewClientAreAccepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.makeRotatableClient(t)
	require.Equal(t, http.StatusOK, h.tokenStatus(t, h.oauthToken(t, c.ClientID, c.Secret)))
}

func TestRotateOAuthClientSecret_AuditedWithoutSecrets(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.makeRotatableClient(t)
	clients, err := h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)
	var oldHash string
	for _, cl := range clients {
		if cl.ID == c.ID {
			oldHash = cl.HashedSecret
		}
	}
	require.NotEmpty(t, oldHash)

	res := h.createCredential(t, rotatePath(c.ID), nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var out struct {
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.Unmarshal(b, &out))

	es := h.entriesFor(t, "oauth_client", c.ID)
	require.Len(t, es, 2, "created, then rotated")
	e := es[0]
	require.Equal(t, "oauth_client_secret_rotated", e.Action)
	require.NotNil(t, e.ActorID)
	require.Equal(t, h.adminID, *e.ActorID, "the signed-in administrator")
	require.Equal(t, "Nightly sync", e.Before["client_name"])
	require.Equal(t, "Nightly sync", e.After["client_name"])
	require.Equal(t, true, e.After["credential_replaced"])

	clients, err = h.authStore.ListOAuthClients(context.Background())
	require.NoError(t, err)
	var newHash string
	for _, cl := range clients {
		if cl.ID == c.ID {
			newHash = cl.HashedSecret
		}
	}
	require.NotEqual(t, oldHash, newHash, "the stored hash was replaced")

	raw := h.rawAudit(t, c.ID)
	for _, secret := range []string{c.Secret, out.ClientSecret, oldHash, newHash} {
		require.NotContains(t, raw, secret, "no secret and no hash of one may reach the audit log")
	}
}

func TestRotateOAuthClientSecret_RefusedRequestsChangeNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.makeRotatableClient(t)
	admin := h.session(t, asAdmin)
	count := func() (n int) {
		require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'oauth_client_secret_rotated'`).Scan(&n))
		return n
	}

	t.Run("unknown client", func(t *testing.T) {
		res := h.doSession(t, admin, http.MethodPost, rotatePath(uuid.New()), nil)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})
	t.Run("malformed id", func(t *testing.T) {
		res := h.doSession(t, admin, http.MethodPost, clientsPath+"/nope/rotate-secret", nil)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})
	t.Run("staff without the credentials resource", func(t *testing.T) {
		res := h.doSession(t, h.session(t, asStaff), http.MethodPost, rotatePath(c.ID), nil)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})
	t.Run("an administrator's API key", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost, rotatePath(c.ID), nil)
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
		require.Equal(t, "session_required", errCode(t, b))
	})
	t.Run("an OAuth client", func(t *testing.T) {
		other := h.newOverScopedServiceClient(t, "other", allScopes()...)
		res := h.doBearer(t, other.Token, http.MethodPost, rotatePath(c.ID), nil)
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
	})

	require.Zero(t, count(), "a refused rotation writes no entry")
	require.Equal(t, http.StatusOK, h.tokenGrantStatus(t, c.ClientID, c.Secret), "and leaves the secret as it was")
}
