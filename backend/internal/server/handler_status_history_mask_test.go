package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
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
//
// What it says instead is "Deleted account" (owner, 2026-11-11), for any role:
// accurate, and the same for staff and requesters, so it tells nobody which.
func TestStatusHistory_DeletedAccountIsMaskedAsDeletedAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		do       func(*harness) func(*testing.T, string, string, any) *http.Response
		id       func(*harness) string
		realName string
	}{
		{"requester", func(h *harness) func(*testing.T, string, string, any) *http.Response { return h.doAsUser },
			func(h *harness) string { return h.userID.String() }, "Reporting User"},
		{"staff", func(h *harness) func(*testing.T, string, string, any) *http.Response { return h.do },
			func(h *harness) string { return h.staffID.String() }, "Staff"},
	} {
		// Every value of the setting: a deleted account's name is not the
		// requester mask's to withhold, so it is withheld whether or not the
		// ticket log masks requesters (admin_log does not).
		for _, setting := range []string{
			admin.MaskRequesterNamesEverywhere, admin.MaskRequesterNamesTicketLog, admin.MaskRequesterNamesAdminLog,
		} {
			t.Run(tc.name+"/"+setting, func(t *testing.T) {
				h, cleanup := newHarness(t)
				defer cleanup()
				ctx := context.Background()
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, setting))
				id := createTicketVia(t, h, tc.do(h))
				deletedID := tc.id(h)
				require.NoError(t, h.userStore.SoftDelete(ctx, uuid.MustParse(deletedID)))

				var seen bool
				for _, r := range statusHistoryAsAdmin(t, h, id) {
					if r.ChangedByUserID == nil || *r.ChangedByUserID != deletedID {
						continue
					}
					seen = true
					require.NotContains(t, r.ChangedByName, tc.realName, "a deleted account's name must not be sent")
					require.Equal(t, "Deleted account", r.ChangedByName)
				}
				require.True(t, seen, "the deleted account's row is on the timeline")
			})
		}
	}
}

// When the role lookup itself fails nothing is known about anyone, so every name
// is withheld as the requester mask, under every value of the setting: a name
// shown "because the setting does not mask" would be a deleted account's, or a
// requester's, shown on the strength of a lookup that did not happen.
func TestStatusHistory_FailedRoleLookupWithholdsEveryNameUnderEverySetting(t *testing.T) {
	for _, setting := range []string{
		admin.MaskRequesterNamesEverywhere, admin.MaskRequesterNamesTicketLog, admin.MaskRequesterNamesAdminLog,
	} {
		t.Run(setting, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyAuditMaskRequesterNames, setting))
			id := createTicketVia(t, h, h.doAsUser)
			h.srv.FailUserBatchLookupForTest(h.userStore)

			var named int
			for _, r := range statusHistoryAsAdmin(t, h, id) {
				if r.ChangedByUserID == nil {
					continue
				}
				named++
				require.Equal(t, "Requester", r.ChangedByName)
				require.True(t, r.ChangedByMasked)
			}
			require.NotZero(t, named, "the timeline has a row with a person on it")
		})
	}
}

// The same for a deleted account in the audit views, whatever its role: the
// name is never sent, and the actor reads "Deleted account".
func TestAudit_DeletedAccountReadsDeletedAccountInBothViews(t *testing.T) {
	for _, tc := range []struct {
		name     string
		do       func(*harness) func(*testing.T, string, string, any) *http.Response
		id       func(*harness) uuid.UUID
		realName string
	}{
		{"requester", func(h *harness) func(*testing.T, string, string, any) *http.Response { return h.doAsUser },
			func(h *harness) uuid.UUID { return h.userID }, "Reporting User"},
		{"staff", func(h *harness) func(*testing.T, string, string, any) *http.Response { return h.do },
			func(h *harness) uuid.UUID { return h.staffID }, "Staff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			id := createTicketVia(t, h, tc.do(h))
			require.NoError(t, h.userStore.SoftDelete(context.Background(), tc.id(h)))

			for _, view := range []string{"admin", "ticket"} {
				e := maskEntryFor(t, maskEntriesFor(t, h, view, id), "created")
				require.NotContains(t, e.ActorName, tc.realName, view)
				require.Equal(t, "Deleted account", e.ActorName, view)
				require.False(t, e.ActorMasked, "it is not the requester mask")
				require.NotNil(t, e.ActorID, "the id stays")
			}
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
