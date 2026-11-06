package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Staff who may see follow entries but not field-level history must still be
// told whose follow an "unfollowed" entry ended. Its actor is whoever
// reassigned the ticket, so without the diff it would read as that person
// stopping.
func TestFollowAudit_AReassignmentEntryNamesWhoseFollowItWas_StaffWithoutHistory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)
	followAs(t, h, h.session(t, asStaff), tk)
	other := seedStaffMember(t, h, "other")
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &other.ID, nil, adminActor(h))
	require.NoError(t, err)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewFollowAudit, true))
	require.False(t, h.adminSvc.StaffCanViewTicketChangeHistory(ctx))

	res := h.doSession(t, h.session(t, asStaff), http.MethodGet, fmt.Sprintf("/api/v1/tickets/%s/audit", tk.ID), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var es []struct {
		Action    string `json:"action"`
		ActorName string `json:"actor_name"`
		Before    map[string]any
		After     map[string]any
		Names     map[string]struct{ Name string }
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&es))

	var assigned, unfollowed = -1, -1
	for i, e := range es {
		switch e.Action {
		case "assigned":
			assigned = i
			require.Nil(t, e.Before, "other entries still get no diff")
		case "unfollowed":
			unfollowed = i
		}
	}
	require.GreaterOrEqual(t, unfollowed, 0)
	require.Greater(t, unfollowed, assigned, "the reassignment is read first")
	e := es[unfollowed]
	require.Equal(t, "Admin", e.ActorName, "the actor is who reassigned")
	require.Equal(t, h.staffID.String(), e.Before["assignee_user_id"], "whose follow it was")
	require.Equal(t, "Staff", e.Names[h.staffID.String()].Name, "named, not just an id")
	require.NotContains(t, e.Before, "following", "nothing beyond the allow-list")
}
