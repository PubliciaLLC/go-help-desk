package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// An OAuth client acts as its service user (#420, decisions on #434).
//
// This test used to be TestOAuthClient_CannotBeTheReporterOrAuthor and pinned
// the opposite. A client's actor then carried no user id, so creating a ticket
// or a reply passed uuid.Nil to the insert, which failed on a foreign key; the
// caller was handed a 500 and every attempt burned a tracking number. The fix
// at the time was to refuse plainly (403 user_identity_required), because the
// credential "was not a person".
//
// That refusal is replaced on purpose: every client now has a user of its own,
// so a client holding tickets:write creates tickets and replies as that user,
// and the audit trail shows the client's name. The scope catalogue always
// advertised tickets:write to clients; this makes it true.
func TestOAuthClient_ActsAsItsServiceUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	c := h.newServiceClient(t, "integration", "tickets:read", "tickets:write")

	t.Run("creating a ticket", func(t *testing.T) {
		res := postBearer(t, h, c.Token, "/api/v1/tickets", map[string]any{
			"subject": "From a robot", "description": "x", "category_id": h.catID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
		var created ticket.Ticket
		require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
		require.NotNil(t, created.ReporterUserID)
		require.Equal(t, c.UserID, *created.ReporterUserID, "the client's service user is the reporter")
	})

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Existing", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	t.Run("adding a reply", func(t *testing.T) {
		res := postBearer(t, h, c.Token,
			"/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "beep"})
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
		var reply ticket.Reply
		require.NoError(t, json.NewDecoder(res.Body).Decode(&reply))
		require.NotNil(t, reply.AuthorID)
		require.Equal(t, c.UserID, *reply.AuthorID, "the client's service user is the author")
	})

	// Every other route that records who acted used to answer 403 for a client.
	// They now run as the client's user, under the role and scopes it has. None
	// may answer 403 user_identity_required, and none may answer 500: the
	// foreign-key failures that refusal replaced must not come back.
	t.Run("the rest of the write routes", func(t *testing.T) {
		cases := []struct {
			name, method, path string
			body               any
		}{
			{"assigning", http.MethodPatch, "/api/v1/tickets/" + tk.ID.String(),
				map[string]any{"assignee_user_id": h.staffID.String()}},
			{"changing the status", http.MethodPatch, "/api/v1/tickets/" + tk.ID.String(),
				map[string]any{"status_id": h.resolvedStatusID(t).String()}},
			{"resolving", http.MethodPost, "/api/v1/tickets/" + tk.ID.String() + "/resolve", map[string]any{}},
			{"reopening", http.MethodPost, "/api/v1/tickets/" + tk.ID.String() + "/reopen", map[string]any{}},
			{"opening a follow-up", http.MethodPost, "/api/v1/tickets/" + tk.ID.String() + "/follow-up", map[string]any{}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res := bearerRequest(t, h, c.Token, tc.method, tc.path, tc.body)
				defer res.Body.Close()
				require.Less(t, res.StatusCode, 500, "an internal error is never the right answer")
				if res.StatusCode == http.StatusForbidden {
					require.NotEqual(t, "user_identity_required", errorCode(t, res),
						"a client has a user identity now")
				}
			})
		}
	})

	// Still gated by scope: a client that was only given read cannot write.
	t.Run("a read-only client cannot write", func(t *testing.T) {
		ro := h.newServiceClient(t, "reader", "tickets:read")
		res := postBearer(t, h, ro.Token, "/api/v1/tickets", map[string]any{
			"subject": "Nope", "description": "x", "category_id": h.catID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "insufficient_scope", errorCode(t, res))
	})
}

// postBearer sends a JSON POST authenticated with an OAuth access token.
func postBearer(t *testing.T, h *harness, token, path string, body any) *http.Response {
	t.Helper()
	return bearerRequest(t, h, token, http.MethodPost, path, body)
}

// bearerRequest sends any JSON request authenticated with an OAuth token.
func bearerRequest(t *testing.T, h *harness, token, method, path string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// resolvedStatusID is the id of a status the ticket can legitimately move to.
func (h *harness) resolvedStatusID(t *testing.T) uuid.UUID {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/statuses", nil)
	defer res.Body.Close()
	var statuses []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&statuses))
	for _, st := range statuses {
		if st.Name == "Resolved" {
			return st.ID
		}
	}
	require.NotEmpty(t, statuses)
	return statuses[0].ID
}
