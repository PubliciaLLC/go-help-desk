package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// serviceClient is an OAuth client created through the admin API, with the
// service user it acts as.
type serviceClient struct {
	ID       uuid.UUID // the client row
	UserID   uuid.UUID // its service user
	ClientID string
	Secret   string
	Token    string // a bearer token already issued for it
}

// newServiceClient creates an OAuth client the way an administrator does and
// returns it along with a token. The user id comes from the client list, which
// is how the admin page learns it.
func (h *harness) newServiceClient(t *testing.T, name string, scopes ...string) serviceClient {
	t.Helper()
	resp := h.createCredential(t, "/api/v1/admin/oauth-clients", map[string]any{
		"name": name, "scopes": scopes,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []struct {
		ID       uuid.UUID `json:"id"`
		ClientID string    `json:"client_id"`
		UserID   uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	for _, c := range list {
		if c.ClientID == created.ClientID {
			return serviceClient{
				ID: c.ID, UserID: c.UserID, ClientID: created.ClientID, Secret: created.ClientSecret,
				Token: h.oauthToken(t, created.ClientID, created.ClientSecret),
			}
		}
	}
	require.FailNow(t, "created client is not in the list")
	return serviceClient{}
}

// sessionCookieFor writes a session row straight into the store and returns
// the cookie for it, skipping every sign-in path. It is how a test builds the
// state a forged or leftover session would be in.
func (h *harness) sessionCookieFor(t *testing.T, sd auth.SessionData) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := h.sessions.New(req, auth.SessionName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = sd
	require.NoError(t, h.sessions.Save(req, rec, sess))
	cookie := rec.Header().Get("Set-Cookie")
	require.NotEmpty(t, cookie)
	return cookie
}

// createTicketID files a ticket as the seeded administrator over HTTP, so
// routing runs, and returns its id.
func createTicketID(t *testing.T, h *harness, subject string) uuid.UUID {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": subject, "description": "x", "category_id": h.catID.String(), "priority": "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	return created.ID
}

// doBearer sends a request carrying an OAuth access token, with a body if one
// is given. (doWithBearer is GET-only.)
func (h *harness) doBearer(t *testing.T, token, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}
