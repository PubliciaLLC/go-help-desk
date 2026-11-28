package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// The ticket list views of #676: scope=assigned_to_me and scope=my_groups,
// held_by, and who may open scope=unassigned (the Available view). The
// fixture is newListFixture (see handler_tickets_filters_test.go):
//
//	a  assigned to staff           e  assigned to staff
//	b  group mine                  f  unassigned, reported by staff
//	c  group other                 g  assigned to admin
//	d  unassigned                  h  group other

const refusedScope = "only admins can use this scope"

// setAvailableRoles stores the setting as the settings handler would.
func setAvailableRoles(t *testing.T, h *harness, raw string) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyAvailableQueueRoles, []byte(raw)))
}

func refused(name, as, query string) listCase {
	return listCase{name: name, as: as, query: query, status: 403, code: "forbidden", message: refusedScope}
}

// Absent, so staff are refused, and an administrator is not.
func TestListTickets_Available_DefaultIsAdminsOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	runListCases(t, h, fx, []listCase{
		refused("staff, key absent", "staff", "?scope=unassigned"),
		refused("requester, key absent", "user", "?scope=unassigned"),
		{name: "admin, key absent", as: "admin", query: "?scope=unassigned", want: "df"},
	})
}

func TestListTickets_Available_FollowsTheSetting(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		staff  bool
	}{
		{"empty list", `[]`, false},
		{"admin only", `["admin"]`, false},
		{"staff ticked", `["staff"]`, true},
		{"both ticked", `["admin","staff"]`, true},
		{"a value of the wrong type fails closed", `{"not":"a list"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			fx := newListFixture(t, h)
			setAvailableRoles(t, h, tc.stored)

			staff := refused("staff", "staff", "?scope=unassigned")
			if tc.staff {
				staff = listCase{name: "staff", as: "staff", query: "?scope=unassigned", want: "df"}
			}
			runListCases(t, h, fx, []listCase{
				staff,
				// An administrator does not depend on what is stored.
				{name: "admin", as: "admin", query: "?scope=unassigned", want: "df"},
				// A requester has no queues, ticked or not.
				refused("requester", "user", "?scope=unassigned"),
			})
		})
	}
}

// Ticking Staff opens Available and nothing else.
func TestListTickets_Available_TickedStaffStillRefusedScopeAll(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	setAvailableRoles(t, h, `["staff"]`)

	runListCases(t, h, fx, []listCase{
		refused("staff scope=all", "staff", "?scope=all"),
		{name: "staff scope=visible", as: "staff", query: "?scope=visible", want: "abcdefgh"},
	})
}

// With enforcement on, Available is the unassigned tickets in the staff
// member's area, or reported by them; with it off, every available ticket.
func TestListTickets_Available_UnderScopeEnforcement(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	setAvailableRoles(t, h, `["staff"]`)

	runListCases(t, h, fx, []listCase{
		{name: "enforcement off: every available ticket", as: "staff", query: "?scope=unassigned", want: "df"},
	})

	enableScope(t, h)
	// The staff member's group covers no category yet: only what they reported.
	runListCases(t, h, fx, []listCase{
		{name: "enforcement on, nothing covered: only what they reported", as: "staff", query: "?scope=unassigned", want: "f"},
	})

	// d is in a category the group now covers, f was reported by them.
	require.NoError(t, h.groupSvc.AddScope(context.Background(), group.GroupScope{GroupID: fx.mine, CategoryID: h.catID}))
	runListCases(t, h, fx, []listCase{
		{name: "enforcement on, covered category", as: "staff", query: "?scope=unassigned", want: "df"},
		{name: "admin is not scoped", as: "admin", query: "?scope=unassigned", want: "df"},
	})
}

// The two new queues, with enforcement off and on.
func TestListTickets_NewQueues(t *testing.T) {
	for _, enforced := range []bool{false, true} {
		name := "enforcement off"
		if enforced {
			name = "enforcement on"
		}
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			fx := newListFixture(t, h)
			if enforced {
				enableScope(t, h)
			}

			runListCases(t, h, fx, []listCase{
				{name: "staff assigned_to_me", as: "staff", query: "?scope=assigned_to_me", want: "ae"},
				{name: "staff my_groups", as: "staff", query: "?scope=my_groups", want: "b"},
				{name: "admin assigned_to_me", as: "admin", query: "?scope=assigned_to_me", want: "g"},
				{name: "admin is in no group", as: "admin", query: "?scope=my_groups", want: ""},
				{name: "assigned_to_me with text", as: "staff", query: "?scope=assigned_to_me&q=llama", want: "e"},
				// A requester has no queues: their own tickets.
				{name: "requester assigned_to_me", as: "user", query: "?scope=assigned_to_me", want: "abcd"},
				{name: "requester my_groups", as: "user", query: "?scope=my_groups", want: "abcd"},

				// Always applied: a named set narrows them and never replaces them.
				{name: "assigned_to_me narrowed by reporter_id", as: "staff", query: "?scope=assigned_to_me&reporter_id={user}", want: "a"},
				{name: "my_groups narrowed by its own group", as: "staff", query: "?scope=my_groups&assignee_group_id={mine}", want: "b"},
			})
		})
	}

	t.Run("a group the caller is not in does not widen my_groups", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		fx := newListFixture(t, h)
		runListCases(t, h, fx, []listCase{
			{name: "staff", as: "staff", query: "?scope=my_groups&assignee_group_id={other}", want: ""},
			{name: "admin", as: "admin", query: "?scope=my_groups&assignee_group_id={other}", want: ""},
		})
	})

	t.Run("enforcement on still refuses a group the staff member is not in", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		fx := newListFixture(t, h)
		enableScope(t, h)
		runListCases(t, h, fx, []listCase{
			{name: "staff", as: "staff", query: "?scope=my_groups&assignee_group_id={other}", status: 403, code: "forbidden", message: "you are not a member of that group"},
		})
	})
}

func TestListTickets_HeldBy(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	runListCases(t, h, fx, []listCase{
		{name: "nobody on my_groups", as: "staff", query: "?scope=my_groups&held_by=nobody", want: "b"},
		{name: "me on my_groups", as: "staff", query: "?scope=my_groups&held_by=me", want: ""},
		{name: "others on my_groups", as: "staff", query: "?scope=my_groups&held_by=others", want: ""},
		{name: "me on visible", as: "staff", query: "?scope=visible&held_by=me", want: "ae"},
		{name: "nobody on visible", as: "staff", query: "?scope=visible&held_by=nobody", want: "bcdfh"},
		{name: "others on visible", as: "staff", query: "?scope=visible&held_by=others", want: "g"},
		{name: "held_by with text", as: "staff", query: "?scope=visible&held_by=nobody&q=llama", want: "fh"},
		{name: "a bad value", as: "staff", query: "?held_by=everyone", status: 400, code: "bad_request", message: "invalid held_by"},
		{name: "a requester", as: "user", query: "?held_by=me", status: 403, code: "forbidden", message: "held_by is not available to requesters"},
	})
}

// An unknown scope is still mine, an empty one too.
func TestListTickets_UnknownScopeIsMine(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	runListCases(t, h, fx, []listCase{
		{name: "unknown", as: "staff", query: "?scope=bogus", want: "abe"},
		{name: "empty", as: "staff", query: "?scope=", want: "abe"},
		{name: "padded known", as: "staff", query: "?scope=%20assigned_to_me%20", want: "ae"},
		{name: "case matters", as: "staff", query: "?scope=ASSIGNED_TO_ME", want: "abe"},
	})
}

// A request that names who it wants is not on a queue, so it is not refused
// for the queue it would have been on (the 1.2 rule, kept).
func TestListTickets_NamedSetStillYieldsForOlderScopes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	runListCases(t, h, fx, []listCase{
		{name: "unticked staff, reporter_id, scope=unassigned", as: "staff", query: "?reporter_id={admin}&scope=unassigned", want: "egh"},
		{name: "unticked staff, reporter_id, scope=all", as: "staff", query: "?reporter_id={admin}&scope=all", want: "egh"},
		{name: "unticked staff, group, scope=unassigned", as: "staff", query: "?assignee_group_id={other}&scope=unassigned", want: "ch"},
	})
}

// can_use_available_queue on GET /me: always present, for every role.
func TestMe_CanUseAvailableQueue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	read := func(do func(*testing.T, string, string, any) *http.Response) (present, value bool) {
		t.Helper()
		res := do(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		raw, ok := body["can_use_available_queue"]
		if ok {
			require.NoError(t, json.Unmarshal(raw, &value))
		}
		return ok, value
	}
	callers := []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{
		{"staff", func(t *testing.T, m, p string, b any) *http.Response { return h.do(t, m, p, b) }},
		{"admin", h.doAsAdmin},
		{"user", h.doAsUser},
	}

	cases := []struct {
		stored             string
		staff, admin, user bool
	}{
		{"", false, true, false},
		{`[]`, false, true, false},
		{`["staff"]`, true, true, false},
		{`["admin"]`, false, true, false},
		{`"staff"`, false, true, false},
	}
	for _, tc := range cases {
		if tc.stored != "" {
			setAvailableRoles(t, h, tc.stored)
		}
		want := map[string]bool{"staff": tc.staff, "admin": tc.admin, "user": tc.user}
		for _, c := range callers {
			present, value := read(c.do)
			require.True(t, present, "%s: always present (stored %q)", c.name, tc.stored)
			require.Equal(t, want[c.name], value, "%s (stored %q)", c.name, tc.stored)
		}
	}
}
