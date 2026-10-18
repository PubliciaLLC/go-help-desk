package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Who sees a service user (#420, decisions on #434): administrators do, on the
// Users page, badged; nobody else does, and no work is ever given to one.

func TestServiceAccount_ListedForAdministratorsWithABadge(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []struct {
		ID             uuid.UUID `json:"id"`
		DisplayName    string    `json:"display_name"`
		Role           string    `json:"role"`
		AuthType       string    `json:"auth_type"`
		ServiceAccount bool      `json:"service_account"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))

	var found bool
	for _, u := range list {
		if u.ID == c.UserID {
			found = true
			require.Equal(t, "Nagios", u.DisplayName, "named after the client")
			require.Equal(t, "staff", u.Role)
			require.Equal(t, "service", u.AuthType)
			require.True(t, u.ServiceAccount)
		} else {
			require.False(t, u.ServiceAccount, "%s is not a service user", u.DisplayName)
		}
	}
	require.True(t, found, "the service user is on the Users page")

	// And its detail view, which is where the page edits it.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users/"+c.UserID.String(), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var detail struct {
		ServiceAccount bool `json:"service_account"`
		HasPassword    bool `json:"has_password"`
		MFAEnabled     bool `json:"mfa_enabled"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&detail))
	require.True(t, detail.ServiceAccount)
	require.False(t, detail.HasPassword)
	require.False(t, detail.MFAEnabled)
}

func TestServiceAccount_CanBeRenamedGivenGroupsAndSwitchedOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	base := "/api/v1/admin/users/" + c.UserID.String()

	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
		map[string]any{"display_name": "Nagios (prod)"}).StatusCode)

	g, err := h.groupSvc.Create(context.Background(), "Monitoring", "")
	require.NoError(t, err)
	resp := h.doAsAdminSession(t, http.MethodPost, "/api/v1/admin/groups/"+g.ID.String()+"/members",
		map[string]any{"user_id": c.UserID.String()})
	require.Less(t, resp.StatusCode, 300, "a service user can be put in a group")

	resp = h.doAsAdmin(t, http.MethodGet, base, nil)
	var detail struct {
		DisplayName string `json:"display_name"`
		Groups      []struct {
			ID uuid.UUID `json:"id"`
		} `json:"groups"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&detail))
	require.Equal(t, "Nagios (prod)", detail.DisplayName)
	require.Len(t, detail.Groups, 1)
	require.Equal(t, g.ID, detail.Groups[0].ID)

	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
		map[string]any{"disabled": true}).StatusCode)
}

func TestServiceAccount_HiddenFromAssigneePickers(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	resp := h.do(t, http.MethodGet, "/api/v1/staff", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var staff []struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&staff))
	var sawStaff bool
	for _, s := range staff {
		require.NotEqual(t, c.UserID, s.ID, "a service user is not somebody to assign work to")
		sawStaff = sawStaff || s.ID == h.staffID
	}
	require.True(t, sawStaff, "the control: ordinary staff are still listed")
}

func TestServiceAccount_CannotBeGivenATicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	id := createTicketID(t, h, "Assign me to a client")

	resp := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/tickets/"+id.String(),
		map[string]any{"assignee_user_id": c.UserID.String()})
	require.GreaterOrEqual(t, resp.StatusCode, 400, "a service user cannot be an assignee")
	require.Less(t, resp.StatusCode, 500)
}

// Round-robin skips it and keeps going, rather than losing its share of the
// tickets or handing them to the client's identity.
func TestServiceAccount_SkippedByRoundRobin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	list, err := json.Marshal([]string{c.UserID.String(), h.staffID.String()})
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyAutoAssignUserIDs, list))

	for i := range 4 {
		id := createTicketID(t, h, fmt.Sprintf("Round robin %d", i))
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id.String(), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var got struct {
			AssigneeUserID *uuid.UUID `json:"assignee_user_id"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		require.NotNil(t, got.AssigneeUserID, "ticket %d is still assigned", i)
		require.Equal(t, h.staffID, *got.AssigneeUserID, "ticket %d", i)
	}
}

// A service user is never mailed. Its address is made up, and a reply that
// asked for a requester copy would otherwise queue mail to it.
func TestServiceAccount_NeverMailedAsRequester(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.dispatcher.record = true
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Filed by a client", Description: "x", CategoryID: h.catID,
		Priority: ticket.PriorityLow, ReporterUserID: &c.UserID,
	})
	require.NoError(t, err)
	h.dispatcher.queued = nil

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "We are on it.", "notify_customer": true})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	require.NotEmpty(t, h.dispatcher.queued, "the reply is announced")
	for _, ev := range h.dispatcher.queued {
		require.Empty(t, ev.Recipient, "event %s must carry no recipient address", ev.Type)
	}
}
