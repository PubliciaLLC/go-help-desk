package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #422: audit diffs carry names beside the raw ids. These tests pin the rules
// the names must not weaken: requester masking, the staff diff setting, and
// staff ticket scope.

type namesEntry struct {
	EntityID string                  `json:"entity_id"`
	Action   string                  `json:"action"`
	Before   map[string]any          `json:"before"`
	After    map[string]any          `json:"after"`
	Names    map[string]namesResolve `json:"names"`
}

type namesResolve struct {
	Name   string `json:"name"`
	Masked bool   `json:"masked"`
}

// plantEntry writes a ticket audit entry directly, so a test controls exactly
// which ids the diff holds.
func plantEntry(t *testing.T, h *harness, ticketID, action string, before, after map[string]any) {
	t.Helper()
	require.NoError(t, h.auditStore.Create(context.Background(), audit.Entry{
		ID: uuid.New(), EntityType: "ticket", EntityID: uuid.MustParse(ticketID),
		Action: action, Before: before, After: after, CreatedAt: time.Now().UTC(),
	}))
}

// namesBody fetches a view as the given caller and returns its raw body and
// the entries for one ticket. do is h.doAsAdmin, h.do (staff) and so on.
func namesView(t *testing.T, h *harness, do func(*testing.T, string, string, any) *http.Response, view, ticketID string) (string, []namesEntry) {
	t.Helper()
	var res *http.Response
	if view == "ticket" {
		res = do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/audit", nil)
	} else {
		res = do(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket&limit=500", nil)
	}
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()

	var all []namesEntry
	if view == "ticket" {
		require.NoError(t, json.Unmarshal([]byte(body), &all))
	} else {
		var page struct {
			Entries []namesEntry `json:"entries"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &page))
		all = page.Entries
	}
	var out []namesEntry
	for _, e := range all {
		if e.EntityID == ticketID || view == "ticket" {
			out = append(out, e)
		}
	}
	return body, out
}

func entryNamed(t *testing.T, es []namesEntry, action string) namesEntry {
	t.Helper()
	for _, e := range es {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("no %q entry in %+v", action, es)
	return namesEntry{}
}

func statusID(t *testing.T, h *harness, name string) string {
	t.Helper()
	st, err := h.ticketStore.GetStatusByName(context.Background(), name)
	require.NoError(t, err)
	return st.ID.String()
}

func TestAuditNames_StatusAndAssigneeResolveBesideTheRawIds(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.doAsUser)
	newID, doneID := statusID(t, h, ticket.StatusNameNew), statusID(t, h, ticket.StatusNameResolved)
	plantEntry(t, h, id, "planted_status",
		map[string]any{"status_id": newID}, map[string]any{"status_id": doneID})
	plantEntry(t, h, id, "planted_unassign",
		map[string]any{"assignee_user_id": h.staffID.String()}, map[string]any{"assignee_user_id": nil})

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			_, es := namesView(t, h, h.doAsAdmin, view, id)

			st := entryNamed(t, es, "planted_status")
			require.Equal(t, newID, st.Before["status_id"], "the raw id stays")
			require.Equal(t, doneID, st.After["status_id"], "the raw id stays")
			require.Equal(t, namesResolve{Name: ticket.StatusNameNew}, st.Names[newID])
			require.Equal(t, namesResolve{Name: ticket.StatusNameResolved}, st.Names[doneID])

			un := entryNamed(t, es, "planted_unassign")
			require.Equal(t, h.staffID.String(), un.Before["assignee_user_id"])
			require.Equal(t, namesResolve{Name: "Staff"}, un.Names[h.staffID.String()])
			require.Len(t, un.Names, 1, "the nil on the other side names nothing")
		})
	}
}

// A row that is gone leaves the id as the only thing to show: no name, and no
// error.
func TestAuditNames_MissingRowsFallBackToTheId(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.doAsUser)
	goneStatus, goneUser := uuid.NewString(), uuid.NewString()
	plantEntry(t, h, id, "planted_gone",
		map[string]any{"status_id": goneStatus, "assignee_user_id": goneUser}, nil)

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			_, es := namesView(t, h, h.doAsAdmin, view, id)
			e := entryNamed(t, es, "planted_gone")
			require.Equal(t, goneStatus, e.Before["status_id"])
			require.Equal(t, goneUser, e.Before["assignee_user_id"])
			require.Empty(t, e.Names)
		})
	}
}

// A soft-deleted account is "gone" to every lookup; the diff must not name it.
func TestAuditNames_DeletedUserIsNotNamed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	u, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "left@names.local", DisplayName: "Leaver Person", Role: user.RoleStaff, Password: "password"})
	require.NoError(t, err)
	id := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, id, "planted_left", map[string]any{"assignee_user_id": u.ID.String()}, nil)
	require.NoError(t, h.userSvc.SoftDelete(ctx, u.ID))

	body, es := namesView(t, h, h.doAsAdmin, "admin", id)
	require.Empty(t, entryNamed(t, es, "planted_left").Names)
	require.NotContains(t, body, "Leaver Person")
}

// Only the fields that are resolved by name are resolved. A user id under any
// other key stays a bare id: a name for it would be a disclosure nobody
// decided on.
func TestAuditNames_OnlyKnownFieldsAreResolved(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, id, "planted_other",
		map[string]any{"follow_up_of": h.staffID.String(), "subject": h.staffID.String()}, nil)

	_, es := namesView(t, h, h.doAsAdmin, "admin", id)
	require.Empty(t, entryNamed(t, es, "planted_other").Names)
}

// The mask: a user the viewer may not see by name stays masked when the name
// would come from the diff rather than from the actor column.
func TestAuditNames_RequesterStaysMaskedWhereTheSettingMasks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	req := h.userID.String()
	plantEntry(t, h, id, "planted_req", map[string]any{"assignee_user_id": req}, nil)

	cases := []struct {
		setting     string
		maskedAdmin bool
		maskedFeed  bool
	}{
		{"", true, true}, // unset reads as everywhere; must run first, the harness keeps what is set
		{admin.MaskRequesterNamesEverywhere, true, true},
		{admin.MaskRequesterNamesAdminLog, true, false},
		{admin.MaskRequesterNamesTicketLog, false, true},
		{"nonsense", true, true}, // unrecognised reads as everywhere
	}
	for _, c := range cases {
		t.Run("setting="+c.setting, func(t *testing.T) {
			if c.setting != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, c.setting))
			}
			for view, masked := range map[string]bool{"admin": c.maskedAdmin, "ticket": c.maskedFeed} {
				body, es := namesView(t, h, h.doAsAdmin, view, id)
				got := entryNamed(t, es, "planted_req").Names[req]
				if masked {
					require.Equal(t, namesResolve{Name: "Requester", Masked: true}, got, view)
					require.NotContains(t, body, "Reporting User", "%s view leaked the requester's name", view)
				} else {
					require.Equal(t, namesResolve{Name: "Reporting User"}, got, view)
				}
			}
		})
	}
}

// Role is read when the entry is viewed (as for actors): a staff member made a
// user is masked, a requester made staff is named.
func TestAuditNames_MaskFollowsTheRoleNow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, id, "planted_role", map[string]any{"assignee_user_id": h.staffID.String()}, nil)

	require.NoError(t, h.userSvc.SetRole(ctx, h.staffID, user.RoleUser))
	_, es := namesView(t, h, h.doAsAdmin, "admin", id)
	require.Equal(t, namesResolve{Name: "Requester", Masked: true},
		entryNamed(t, es, "planted_role").Names[h.staffID.String()])
}

// The staff diff setting: with it off a staff member gets no diff and so no
// names; names are derived from the diff that is shown, never beside it.
func TestAuditNames_StaffDiffSettingStillGatesThem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := createTicketVia(t, h, h.do)
	newID := statusID(t, h, ticket.StatusNameNew)
	plantEntry(t, h, id, "planted_status", map[string]any{"status_id": newID}, nil)

	for _, view := range []string{"admin", "ticket"} {
		body, es := namesView(t, h, h.do, view, id)
		e := entryNamed(t, es, "planted_status")
		require.Empty(t, e.Before, view)
		require.Empty(t, e.Names, view)
		require.NotContains(t, body, `"names"`, view)
		require.NotContains(t, body, newID, view)
	}

	// Reporting users never see a diff, and so never see names, setting or not.
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewTicketChangeHistory, true))
	own := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, own, "planted_status", map[string]any{"status_id": newID}, nil)
	body, _ := namesView(t, h, h.doAsUser, "ticket", own)
	require.NotContains(t, body, `"names"`)
	require.NotContains(t, body, newID)

	for _, view := range []string{"admin", "ticket"} {
		_, es := namesView(t, h, h.do, view, id)
		e := entryNamed(t, es, "planted_status")
		require.Equal(t, newID, e.Before["status_id"], view)
		require.Equal(t, namesResolve{Name: ticket.StatusNameNew}, e.Names[newID], view)
	}
}

// Scope (#391): the lookup runs over the entries the viewer is already allowed
// to see, so a name never reaches a staff member through an entry outside
// their scope, and the page and total are unchanged by it.
func TestAuditNames_ScopedStaffGetNothingBeyondTheirScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewTicketChangeHistory, true))
	enableScope(t, h)

	hidden, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "zed@names.local", DisplayName: "Zed Hidden", Role: user.RoleStaff, Password: "password"})
	require.NoError(t, err)
	inScope := createTicketVia(t, h, h.do) // reported by the staff member
	outScope := createTicketVia(t, h, h.doAsUser)
	plantEntry(t, h, inScope, "planted_in", map[string]any{"assignee_user_id": h.adminID.String()}, nil)
	plantEntry(t, h, outScope, "planted_out", map[string]any{"assignee_user_id": hidden.ID.String()}, nil)

	body, es := namesView(t, h, h.do, "admin", inScope)
	require.Equal(t, namesResolve{Name: "Admin"}, entryNamed(t, es, "planted_in").Names[h.adminID.String()])
	require.NotContains(t, body, "Zed Hidden")
	require.NotContains(t, body, hidden.ID.String())
	require.NotContains(t, body, "planted_out")

	// The ticket the staff member cannot see is a 404 on the feed, as before.
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+outScope+"/audit", nil)
	b, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.False(t, strings.Contains(b, "Zed Hidden"))
}
