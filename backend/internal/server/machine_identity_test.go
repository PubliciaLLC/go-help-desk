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

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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
	// They now run as the client's user, so each must SUCCEED, with the code a
	// staff member would get. Each gets a ticket in the state it needs, so the
	// success code is the route's own and not a refusal that happens to be a 4xx.
	t.Run("the rest of the write routes succeed", func(t *testing.T) {
		fresh := func(t *testing.T) ticket.Ticket {
			t.Helper()
			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "Fresh", Description: "x", CategoryID: h.catID, ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)
			return tk
		}
		// A ticket the client itself reported, then closed by an administrator.
		closedClientTicket := func(t *testing.T) ticket.Ticket {
			t.Helper()
			res := postBearer(t, h, c.Token, "/api/v1/tickets", map[string]any{
				"subject": "Mine", "description": "x", "category_id": h.catID.String(),
			})
			var tk ticket.Ticket
			require.NoError(t, json.NewDecoder(res.Body).Decode(&tk))
			res.Body.Close()
			res = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/close", map[string]any{})
			res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
			return tk
		}
		// Reopening a Closed ticket is off by default (closed_reopen_policy);
		// allowing staff is how a client that is staff is permitted to.
		require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyClosedReopenPolicy, string(ticket.ReopenPolicyStaffAdmin)))

		cases := []struct {
			name, method, suffix string
			body                 any
			setup                func(*testing.T) ticket.Ticket
			want                 int
		}{
			{"assigning", http.MethodPatch, "", map[string]any{"assignee_user_id": h.staffID.String()}, fresh, http.StatusOK},
			{"changing the status", http.MethodPatch, "", map[string]any{"status_id": h.resolvedStatusID(t).String()}, fresh, http.StatusOK},
			{"resolving", http.MethodPost, "/resolve", map[string]any{}, fresh, http.StatusOK},
			{"reopening", http.MethodPost, "/reopen", map[string]any{}, closedClientTicket, http.StatusOK},
			{"opening a follow-up", http.MethodPost, "/follow-up", map[string]any{}, closedClientTicket, http.StatusCreated},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tk := tc.setup(t)
				res := bearerRequest(t, h, c.Token, tc.method, "/api/v1/tickets/"+tk.ID.String()+tc.suffix, tc.body)
				defer res.Body.Close()
				require.Equal(t, tc.want, res.StatusCode)
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
