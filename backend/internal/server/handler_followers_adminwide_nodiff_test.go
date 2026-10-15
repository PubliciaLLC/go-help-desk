package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The admin-wide view gives the same: staff who may see follow entries but not
// change history get the reassignment unfollow's before and after, with the
// name resolved, and no other entry gains one.
func TestFollowAudit_AdminWideView_ReassignmentEntryNamesWhoseFollowItWas_StaffWithoutHistory(t *testing.T) {
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

	res := h.doSession(t, h.session(t, asStaff), http.MethodGet, "/api/v1/admin/audit?entity_type=ticket", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var body struct {
		Entries []struct {
			Action    string `json:"action"`
			ActorName string `json:"actor_name"`
			Before    map[string]any
			After     map[string]any
			Names     map[string]struct{ Name string }
		} `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))

	found := 0
	for _, e := range body.Entries {
		switch e.Action {
		case "unfollowed":
			found++
			require.Equal(t, "Admin", e.ActorName)
			require.Equal(t, h.staffID.String(), e.Before["assignee_user_id"])
			require.Equal(t, "Staff", e.Names[h.staffID.String()].Name)
			require.NotContains(t, e.Before, "following")
		case "followed":
			// Nothing on it is allow-listed, so nothing is sent.
		default:
			require.Nil(t, e.Before, "%s: no other entry gains a diff", e.Action)
			require.Nil(t, e.After, "%s: no other entry gains a diff", e.Action)
			require.Empty(t, e.Names)
		}
	}
	require.Equal(t, 1, found)
}
