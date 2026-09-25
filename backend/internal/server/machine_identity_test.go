package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A credential with nobody behind it is told so, not handed a 500.
//
// An OAuth client actor carries no user id. Creating a ticket or a reply then
// passed uuid.Nil to the insert, which failed on a foreign key: the caller
// got "an internal error occurred", the log got a constraint name, and the
// create path had already taken a tracking number from the sequence — so
// every attempt left a hole in the numbering. The scope catalogue advertises
// tickets:write to OAuth clients, so this is a capability the API offers and
// cannot deliver.
//
// Refusing plainly is the honest answer: the request is not malformed and the
// server is not broken, the credential simply is not a person.
func TestOAuthClient_CannotBeTheReporterOrAuthor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	created := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/oauth-clients", map[string]any{
		"name": "integration", "scopes": []string{"tickets:read", "tickets:write"},
	})
	require.Equal(t, http.StatusCreated, created.StatusCode)
	var client struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&client))
	created.Body.Close()

	token := h.oauthToken(t, client.ClientID, client.ClientSecret)

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Existing", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	before := latestTicketNumber(t, h)

	t.Run("creating a ticket", func(t *testing.T) {
		res := postBearer(t, h, token, "/api/v1/tickets", map[string]any{
			"subject": "From a robot", "description": "x", "category_id": h.catID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a credential with no user identity should be refused, not answered with an internal error")
	})

	t.Run("adding a reply", func(t *testing.T) {
		res := postBearer(t, h, token,
			"/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "beep"})
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	// And the refused create did not spend a tracking number.
	after := createTicketAsUser(t, h, "After the refusal")
	require.Equal(t, before+1, trackingSeq(t, after.TrackingNumber),
		"a refused create consumed a tracking number")
}

// postBearer sends a JSON POST authenticated with an OAuth access token.
func postBearer(t *testing.T, h *harness, token, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}
