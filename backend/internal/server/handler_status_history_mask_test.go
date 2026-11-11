package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

type historyRow struct {
	ChangedByUserID *string `json:"changed_by_user_id"`
	ChangedByName   string  `json:"changed_by_name"`
	ChangedByMasked bool    `json:"changed_by_masked"`
}

func statusHistoryAsAdmin(t *testing.T, h *harness, ticketID string) []historyRow {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/history", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var rows []historyRow
	decodeJSON(t, res, &rows)
	res.Body.Close()
	return rows
}

// D35: the status timeline says who changed a ticket's status, which is audit
// output like the Activity feed beside it, so it masks a requester's name under
// the same setting and the same per-view rule (the ticket log).
func TestStatusHistory_RequesterNameIsMaskedLikeTheActivityFeed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)

	for _, tc := range []struct {
		setting    string
		wantMasked bool
	}{
		{admin.MaskRequesterNamesEverywhere, true},
		{admin.MaskRequesterNamesTicketLog, true},
		{admin.MaskRequesterNamesAdminLog, false}, // the admin-wide log only
	} {
		t.Run(tc.setting, func(t *testing.T) {
			require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, tc.setting))
			var requesterRows int
			for _, r := range statusHistoryAsAdmin(t, h, id) {
				if r.ChangedByUserID == nil || *r.ChangedByUserID != h.userID.String() {
					continue
				}
				requesterRows++
				require.Equal(t, tc.wantMasked, r.ChangedByMasked)
				if tc.wantMasked {
					require.Equal(t, "Requester", r.ChangedByName)
				} else {
					require.Equal(t, "Reporting User", r.ChangedByName)
				}
			}
			require.NotZero(t, requesterRows, "the requester's own status row is on the timeline")
		})
	}
}

// The mask fails closed: a name is shown only for an account positively known
// to be staff or an administrator. A requester whose account has since been
// deleted is not found by the role lookup, and their name is still on the
// timeline (it comes from the row, not from the lookup), so "not found" must
// mean "mask", never "show".
func TestStatusHistory_DeletedRequesterIsStillMasked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	require.NoError(t, h.userStore.SoftDelete(ctx, h.userID))

	var seen bool
	for _, r := range statusHistoryAsAdmin(t, h, id) {
		if r.ChangedByUserID == nil || *r.ChangedByUserID != h.userID.String() {
			continue
		}
		seen = true
		require.NotContains(t, r.ChangedByName, "Reporting User", "the deleted requester's name must not be sent")
		require.Equal(t, "Requester", r.ChangedByName)
		require.True(t, r.ChangedByMasked)
	}
	require.True(t, seen, "the deleted requester's row is on the timeline")
}

// The same for a deleted account in the audit views: the name is never sent for
// an account the lookup cannot find (the actor is left unnamed, not named).
func TestAudit_DeletedRequesterIsNeverNamed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	require.NoError(t, h.userStore.SoftDelete(ctx, h.userID))

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			e := maskEntryFor(t, maskEntriesFor(t, h, view, id), "created")
			require.NotContains(t, e.ActorName, "Reporting User")
		})
	}
}

// Staff are named, whatever the setting.
func TestStatusHistory_StaffAreStillNamed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.do)
	var seen bool
	for _, r := range statusHistoryAsAdmin(t, h, id) {
		if r.ChangedByUserID != nil && *r.ChangedByUserID == h.staffID.String() {
			seen = true
			require.Equal(t, "Staff", r.ChangedByName)
			require.False(t, r.ChangedByMasked)
		}
	}
	require.True(t, seen)
}
