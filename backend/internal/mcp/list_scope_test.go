package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// list_tickets takes `scope` and `held_by` (#676). The queue comes from
// ticket.ResolveQueue, the one rule GET /tickets uses, so a refusal is REST's
// refusal in REST's words. An absent scope is `visible`, which is what this
// tool has always listed; an unknown one is refused, never skipped.

const refusedScope = "only admins can use this scope"

func TestListFilterFor_Scope(t *testing.T) {
	staff, admin, requester := user.RoleStaff, user.RoleAdmin, user.RoleUser
	cases := []struct {
		name      string
		role      user.Role
		vis       ticket.Visibility
		available bool
		args      map[string]any
		want      ticket.Queue
		refused   string // the FilterForbidden text, when the call is refused
	}{
		// Absent or null: visible, so the queue is "any" for everybody.
		{"absent, staff", staff, ticket.VisibilityAll, false, map[string]any{}, ticket.QueueAny, ""},
		{"absent, scoped staff", staff, ticket.VisibilityScoped, false, map[string]any{}, ticket.QueueAny, ""},
		{"absent, admin", admin, ticket.VisibilityAll, false, map[string]any{}, ticket.QueueAny, ""},
		{"absent, requester", requester, ticket.VisibilityReporter, false, map[string]any{}, ticket.QueueAny, ""},
		{"null, staff", staff, ticket.VisibilityAll, false, map[string]any{"scope": nil}, ticket.QueueAny, ""},

		{"visible", staff, ticket.VisibilityAll, false, map[string]any{"scope": "visible"}, ticket.QueueAny, ""},
		{"mine, staff", staff, ticket.VisibilityAll, false, map[string]any{"scope": "mine"}, ticket.QueueMine, ""},
		{"mine, scoped staff gets what the scope admits", staff, ticket.VisibilityScoped, false, map[string]any{"scope": "mine"}, ticket.QueueAny, ""},
		{"assigned_to_me, staff", staff, ticket.VisibilityAll, false, map[string]any{"scope": "assigned_to_me"}, ticket.QueueAssignedToMe, ""},
		{"assigned_to_me, scoped staff", staff, ticket.VisibilityScoped, false, map[string]any{"scope": "assigned_to_me"}, ticket.QueueAssignedToMe, ""},
		{"assigned_to_me, admin", admin, ticket.VisibilityAll, false, map[string]any{"scope": "assigned_to_me"}, ticket.QueueAssignedToMe, ""},
		{"my_groups, staff", staff, ticket.VisibilityAll, false, map[string]any{"scope": "my_groups"}, ticket.QueueMyGroups, ""},
		{"my_groups, scoped staff", staff, ticket.VisibilityScoped, false, map[string]any{"scope": "my_groups"}, ticket.QueueMyGroups, ""},
		{"my_groups, admin", admin, ticket.VisibilityAll, false, map[string]any{"scope": "my_groups"}, ticket.QueueMyGroups, ""},

		// Available: admin always, staff when ticked.
		{"unassigned, admin unticked", admin, ticket.VisibilityAll, false, map[string]any{"scope": "unassigned"}, ticket.QueueUnassigned, ""},
		{"unassigned, staff ticked", staff, ticket.VisibilityAll, true, map[string]any{"scope": "unassigned"}, ticket.QueueUnassigned, ""},
		{"unassigned, scoped staff ticked", staff, ticket.VisibilityScoped, true, map[string]any{"scope": "unassigned"}, ticket.QueueUnassigned, ""},
		{"unassigned, staff unticked", staff, ticket.VisibilityAll, false, map[string]any{"scope": "unassigned"}, 0, refusedScope},
		{"unassigned, scoped staff unticked", staff, ticket.VisibilityScoped, false, map[string]any{"scope": "unassigned"}, 0, refusedScope},
		{"unassigned, requester", requester, ticket.VisibilityReporter, false, map[string]any{"scope": "unassigned"}, 0, refusedScope},

		// All: admin only, ticked or not.
		{"all, admin", admin, ticket.VisibilityAll, false, map[string]any{"scope": "all"}, ticket.QueueAny, ""},
		{"all, staff ticked", staff, ticket.VisibilityAll, true, map[string]any{"scope": "all"}, 0, refusedScope},
		{"all, staff", staff, ticket.VisibilityAll, false, map[string]any{"scope": "all"}, 0, refusedScope},
		{"all, requester", requester, ticket.VisibilityReporter, true, map[string]any{"scope": "all"}, 0, refusedScope},

		// A requester has no queues: their own tickets, whatever else they ask for.
		{"mine, requester", requester, ticket.VisibilityReporter, false, map[string]any{"scope": "mine"}, ticket.QueueAny, ""},
		{"assigned_to_me, requester", requester, ticket.VisibilityReporter, false, map[string]any{"scope": "assigned_to_me"}, ticket.QueueAny, ""},
		{"my_groups, requester", requester, ticket.VisibilityReporter, false, map[string]any{"scope": "my_groups"}, ticket.QueueAny, ""},
		{"visible, requester", requester, ticket.VisibilityReporter, false, map[string]any{"scope": "visible"}, ticket.QueueAny, ""},

		// The 1.2 rule: naming who you want is not being on a queue, for the four
		// older values. The two new ones are always applied.
		{"unassigned yields to reporter_id", staff, ticket.VisibilityAll, false,
			map[string]any{"scope": "unassigned", "reporter_id": uuid.NewString()}, ticket.QueueAny, ""},
		{"all yields to requester text", staff, ticket.VisibilityAll, false,
			map[string]any{"scope": "all", "requester": "whitfield"}, ticket.QueueAny, ""},
		{"mine yields to a group", staff, ticket.VisibilityAll, false,
			map[string]any{"scope": "mine", "assignee_group_id": uuid.NewString()}, ticket.QueueAny, ""},
		{"my_groups is narrowed by a group, not replaced", staff, ticket.VisibilityAll, false,
			map[string]any{"scope": "my_groups", "assignee_group_id": uuid.NewString()}, ticket.QueueMyGroups, ""},
		{"assigned_to_me is narrowed by reporter_id, not replaced", staff, ticket.VisibilityAll, false,
			map[string]any{"scope": "assigned_to_me", "reporter_id": uuid.NewString()}, ticket.QueueAssignedToMe, ""},
		// Kept from before: a staff member outside the group may name it.
		{"a group is not refused to staff outside it", staff, ticket.VisibilityScoped, false,
			map[string]any{"assignee_group_id": uuid.NewString()}, ticket.QueueAny, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := listFilterFor(actorWith(tc.role, false), tc.vis, tc.available, tc.args)
			if tc.refused != "" {
				var fe *ticket.FilterError
				require.ErrorAs(t, err, &fe)
				require.Equal(t, ticket.FilterForbidden, fe.Kind)
				require.Equal(t, tc.refused, fe.Msg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, f.Queue)
			require.Equal(t, tc.vis, f.Visibility, "a queue never changes what the caller may see")
		})
	}
}

// An unknown or mistyped scope is refused, in the words every other argument of
// this tool uses, and is a caller's mistake (ErrValidation), not a server error.
func TestListFilterFor_ScopeInvalid(t *testing.T) {
	cases := []struct {
		name  string
		scope any
	}{
		{"unknown", "everything"},
		{"empty", ""},
		{"blank", "   "},
		{"case matters", "MINE"},
		{"a number", 1.0},
		{"a bool", true},
		{"an array", []any{"mine"}},
		{"an object", map[string]any{"scope": "mine"}},
	}
	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin, user.RoleUser} {
		for _, tc := range cases {
			t.Run(string(role)+"/"+tc.name, func(t *testing.T) {
				_, err := listFilterFor(actorWith(role, false), ticket.VisibilityAll, true, map[string]any{"scope": tc.scope})
				require.ErrorIs(t, err, ticket.ErrValidation)
				require.Contains(t, err.Error(), "invalid scope")
			})
		}
	}
}

// held_by is read with the other string filters, by the one parser REST uses.
func TestListFilterFor_HeldBy(t *testing.T) {
	cases := []struct {
		name    string
		role    user.Role
		vis     ticket.Visibility
		value   any
		want    ticket.HeldBy
		invalid bool
		refused string
	}{
		{"absent", user.RoleStaff, ticket.VisibilityAll, nil, "", false, ""},
		{"nobody", user.RoleStaff, ticket.VisibilityAll, "nobody", ticket.HeldNobody, false, ""},
		{"me", user.RoleStaff, ticket.VisibilityAll, "me", ticket.HeldMe, false, ""},
		{"others", user.RoleAdmin, ticket.VisibilityAll, "others", ticket.HeldOthers, false, ""},
		{"unknown value", user.RoleStaff, ticket.VisibilityAll, "everyone", "", true, ""},
		{"wrong type", user.RoleStaff, ticket.VisibilityAll, 1.0, "", true, ""},
		{"requester", user.RoleUser, ticket.VisibilityReporter, "me", "", false, "held_by is not available to requesters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"held_by": tc.value}
			f, err := listFilterFor(actorWith(tc.role, false), tc.vis, false, args)
			switch {
			case tc.invalid:
				require.ErrorIs(t, err, ticket.ErrValidation)
				require.Contains(t, err.Error(), "invalid held_by")
			case tc.refused != "":
				var fe *ticket.FilterError
				require.ErrorAs(t, err, &fe)
				require.Equal(t, ticket.FilterForbidden, fe.Kind)
				require.Equal(t, tc.refused, fe.Msg)
			default:
				require.NoError(t, err)
				require.Equal(t, tc.want, f.HeldBy)
			}
		})
	}
}

// Both refusals are answered, in REST's words, and neither reaches the store or
// the error log.
func TestHandleListTickets_ScopeRefusalsAreAnswered(t *testing.T) {
	cases := []struct {
		name      string
		role      user.Role
		vis       ticket.Visibility
		available bool
		args      map[string]any
		want      string
	}{
		{"unticked staff, unassigned", user.RoleStaff, ticket.VisibilityAll, false, map[string]any{"scope": "unassigned"}, refusedScope},
		{"ticked staff, all", user.RoleStaff, ticket.VisibilityAll, true, map[string]any{"scope": "all"}, refusedScope},
		{"requester, unassigned", user.RoleUser, ticket.VisibilityReporter, false, map[string]any{"scope": "unassigned"}, refusedScope},
		{"requester, held_by", user.RoleUser, ticket.VisibilityReporter, false, map[string]any{"held_by": "nobody"}, "held_by is not available to requesters"},
		{"unknown scope", user.RoleStaff, ticket.VisibilityAll, true, map[string]any{"scope": "bogus"}, "invalid scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			s := &Server{authz: &recordingAuthorizer{visibility: tc.vis, available: tc.available}}
			ctx := context.WithValue(context.Background(), actorCtxKey{}, actorWith(tc.role, false))
			res, err := s.handleListTickets(ctx, callToolRequest("list_tickets", tc.args))
			require.NoError(t, err)
			require.True(t, res.IsError)
			require.Contains(t, resultText(t, res), tc.want)
			require.NotContains(t, resultText(t, res), "list tickets failed")
			require.NotContains(t, logs.String(), "level=ERROR")
		})
	}
}

// No Authorizer means no Available queue, like it means nothing visible.
func TestDeniedAuthorizer_AvailableQueueIsClosed(t *testing.T) {
	var a Authorizer = deniedAuthorizer{}
	for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		require.False(t, a.AvailableQueueAllowed(context.Background(), &authmw.Actor{Role: role}))
	}
}

func TestToolSchema_ListTicketsAdvertisesScopeAndHeldBy(t *testing.T) {
	s := New(nil, nil, nil, nil, nil, nil)
	tool := s.mcp.GetTool("list_tickets")
	require.NotNil(t, tool)
	props := tool.Tool.InputSchema.Properties

	scope, ok := props["scope"].(map[string]any)
	require.True(t, ok, "scope is advertised")
	require.Equal(t, "string", scope["type"])
	require.ElementsMatch(t,
		[]any{"mine", "assigned_to_me", "my_groups", "unassigned", "all", "visible"}, scope["enum"])
	require.Contains(t, scope["description"], "absent means visible")

	held, ok := props["held_by"].(map[string]any)
	require.True(t, ok, "held_by is advertised")
	require.Equal(t, "string", held["type"])
	require.ElementsMatch(t, []any{"nobody", "me", "others"}, held["enum"])
}
