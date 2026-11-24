package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Deleting an OAuth client was the only revocation this system offered, and it
// revoked nothing: BearerAuth verified the JWT signature and never asked whether
// the client still existed, so a token kept working for its full hour.
//
// GHSA-p2wx-wvfr-7h3p tells operators that removing access cuts it off.
func TestDeletedOAuthClient_TokenStopsWorking(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/oauth-clients", map[string]any{
		"name": "integration", "scopes": []string{"tickets:read"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	token := h.oauthToken(t, created.ClientID, created.ClientSecret)

	// Baseline, so a later 401 is the revocation rather than a broken fixture.
	require.Equal(t, http.StatusOK, h.doWithBearer(t, token, "/api/v1/tickets").StatusCode)

	// Find and delete it.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"client_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&clients))
	var id string
	for _, c := range clients {
		if c.ClientID == created.ClientID {
			id = c.ID
		}
	}
	require.NotEmpty(t, id, "created client should be listed")

	resp = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/oauth-clients/"+id, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// The token is still cryptographically valid and unexpired. It must not work.
	require.Equal(t, http.StatusUnauthorized,
		h.doWithBearer(t, token, "/api/v1/tickets").StatusCode,
		"a deleted client's token must stop working immediately, not in an hour")
}

// The disable/login race: VerifyPassword checks IsActive, then the session is
// written ~30 lines later. A disable landing in between deleted no sessions,
// because the session did not exist yet — leaving a live session for a disabled
// user that nothing would ever revoke.
//
// Deciding it in the session lookup removes the window rather than narrowing it:
// the row simply does not load for a disabled user, whenever it was written.
func TestDisabledUser_SessionWrittenBeforeDisable_DoesNotAuthenticate(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A session for the staff user, written directly — this is the state the
	// race produces, without having to win the race.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := h.sessions.New(req, auth.SessionName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = auth.SessionData{
		UserID: h.staffID, Role: user.RoleStaff, MFAPassed: true,
	}
	require.NoError(t, h.sessions.Save(req, rec, sess))
	cookie := rec.Header().Get("Set-Cookie")
	require.NotEmpty(t, cookie)

	require.Equal(t, http.StatusOK, h.doWithCookie(t, cookie, "/api/v1/tickets").StatusCode,
		"the session should work before the disable")

	// Disable the user WITHOUT going through the handler, so no DeleteForUser
	// runs — exactly what the race leaves behind.
	require.NoError(t, h.userSvc.Disable(context.Background(), h.staffID))

	require.Equal(t, http.StatusUnauthorized,
		h.doWithCookie(t, cookie, "/api/v1/tickets").StatusCode,
		"a disabled user's session must not authenticate even if it was never deleted")
}

// Pre-authentication sessions carry OIDC state (nonce, PKCE verifier) and have
// no user. An inner join would drop them and break the login flow the change is
// meant to protect.
func TestPreAuthSession_WithNoUser_StillLoads(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := h.sessions.New(req, auth.SessionName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = auth.SessionData{OIDCNonce: "n-0S6_WzA2Mj"}
	require.NoError(t, h.sessions.Save(req, rec, sess))

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
	loaded, err := h.sessions.Get(req2, auth.SessionName)
	require.NoError(t, err)
	sd, ok := loaded.Values[auth.SessionDataKey].(auth.SessionData)
	require.True(t, ok, "a session with no user must still load, or OIDC login breaks")
	require.Equal(t, "n-0S6_WzA2Mj", sd.OIDCNonce)
}
