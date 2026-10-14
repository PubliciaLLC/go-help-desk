package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The admin API for OAuth clients, now that each one acts through a service
// user (#420).

func TestOAuthClients_CreateMakesItsServiceUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/oauth-clients", map[string]any{
		"name": "Nagios", "scopes": []string{"tickets:read"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		UserID uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEqual(t, uuid.Nil, created.UserID, "the response names the service user")

	u, err := h.userSvc.GetByIDAdmin(context.Background(), created.UserID)
	require.NoError(t, err)
	require.True(t, u.ServiceAccount)
	require.Equal(t, "Nagios", u.DisplayName)
	require.EqualValues(t, "staff", u.Role)
}

func TestOAuthClients_ListShowsUserAndGroups(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	grouped := h.newServiceClient(t, "Grouped", "tickets:read")
	bare := h.newServiceClient(t, "Bare", "tickets:read")

	g, err := h.groupSvc.Create(ctx, "Monitoring", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, grouped.UserID))

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rows []struct {
		ID     uuid.UUID `json:"id"`
		UserID uuid.UUID `json:"user_id"`
		Groups []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"groups"`
	}
	raw := json.RawMessage{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.NotContains(t, string(raw), "hashed_secret")

	by := map[uuid.UUID]int{}
	for i, r := range rows {
		by[r.ID] = i
	}
	gr := rows[by[grouped.ID]]
	require.Equal(t, grouped.UserID, gr.UserID)
	require.Len(t, gr.Groups, 1)
	require.Equal(t, g.ID, gr.Groups[0].ID)
	require.Equal(t, "Monitoring", gr.Groups[0].Name)

	br := rows[by[bare.ID]]
	require.Equal(t, bare.UserID, br.UserID)
	require.NotNil(t, br.Groups, `"no groups" is [] and not null`)
	require.Empty(t, br.Groups)
}

func TestOAuthClients_DeleteSoftDeletesTheUserAndKeepsWhatItWrote(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")

	res := postBearer(t, h, c.Token, "/api/v1/tickets", map[string]any{
		"subject": "Disk almost full", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var tk struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&tk))

	resp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/oauth-clients/"+c.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// The user is gone from every live view, but the row is kept.
	_, err := h.userSvc.GetByID(ctx, c.UserID)
	require.Error(t, err)
	u, err := h.userSvc.GetByIDAdmin(ctx, c.UserID)
	require.NoError(t, err)
	require.NotNil(t, u.DeletedAt)

	// The ticket it filed is still there, still reported by it.
	got := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String(), nil)
	require.Equal(t, http.StatusOK, got.StatusCode)
	var after struct {
		ReporterUserID uuid.UUID `json:"reporter_user_id"`
	}
	require.NoError(t, json.NewDecoder(got.Body).Decode(&after))
	require.Equal(t, c.UserID, after.ReporterUserID)

	// And a second delete is a 404, as before.
	resp = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/oauth-clients/"+c.ID.String(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Erik's Q6: a service user is renamed, regrouped and disabled from the Users
// page, but it is deleted only by deleting its client.
func TestServiceAccount_RenameRenamesTheClient(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	other := h.newServiceClient(t, "Zapier", "tickets:read")

	resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+c.UserID.String(),
		map[string]any{"display_name": "Nagios (prod)"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/oauth-clients", nil)
	var rows []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rows))
	names := map[uuid.UUID]string{}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	require.Equal(t, "Nagios (prod)", names[c.ID], "the client takes the new name")
	require.Equal(t, "Zapier", names[other.ID], "and only that client")
}

func TestServiceAccount_CannotBeDeletedExceptThroughItsClient(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	resp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/users/"+c.UserID.String(), nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
	require.Equal(t, "service_account", e.Error.Code)
	require.Equal(t, "This is the service account for OAuth client Nagios. Delete the client instead.", e.Error.Message)

	// Nothing happened: the user is live and the client still works.
	u, err := h.userSvc.GetByID(context.Background(), c.UserID)
	require.NoError(t, err)
	require.Nil(t, u.DeletedAt)
	require.Equal(t, http.StatusOK, h.doWithBearer(t, c.Token, "/api/v1/me").StatusCode)
}

// Disabling the service user from the Users page stops the client. Enabling it
// starts it again.
func TestOAuthClients_DisablingTheUserStopsTheClient(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	base := "/api/v1/admin/users/" + c.UserID.String()

	require.Equal(t, http.StatusOK, h.doWithBearer(t, c.Token, "/api/v1/me").StatusCode)

	require.Equal(t, http.StatusOK, h.doAsAdmin(t, http.MethodPatch, base, map[string]any{"disabled": true}).StatusCode)
	require.Equal(t, http.StatusUnauthorized, h.doWithBearer(t, c.Token, "/api/v1/me").StatusCode,
		"a disabled service user's client is switched off")

	require.Equal(t, http.StatusOK, h.doAsAdmin(t, http.MethodPatch, base, map[string]any{"disabled": false}).StatusCode)
	require.Equal(t, http.StatusOK, h.doWithBearer(t, c.Token, "/api/v1/me").StatusCode)
}
