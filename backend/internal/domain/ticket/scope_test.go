package ticket

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func TestParseScope(t *testing.T) {
	cases := []struct {
		in   string
		want Scope
		ok   bool
	}{
		{"mine", ScopeMine, true},
		{"assigned_to_me", ScopeAssignedToMe, true},
		{"my_groups", ScopeMyGroups, true},
		{"unassigned", ScopeUnassigned, true},
		{"all", ScopeAll, true},
		{"visible", ScopeVisible, true},
		{" my_groups ", ScopeMyGroups, true},
		{"\tall\n", ScopeAll, true},
		{"", "", false},
		{"   ", "", false},
		{"bogus", "", false},
		{"Mine", "", false},
		{"my groups", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := ParseScope(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveQueue(t *testing.T) {
	const forbidden = "only admins can use this scope"
	id := uuid.New()

	named := map[string]Filter{
		"none":      {},
		"reporter":  {ReporterID: &id},
		"requester": {Requester: "ann"},
		"group":     {AssigneeGroupID: &id},
	}
	roles := []user.Role{user.RoleUser, user.RoleStaff, user.RoleAdmin}
	visibilities := []Visibility{VisibilityReporter, VisibilityScoped, VisibilityAll}

	// want is the answer for one combination, written out independently of
	// the implementation's table.
	want := func(s Scope, role user.Role, vis Visibility, availOK, isNamed bool) (Queue, string) {
		switch s {
		case ScopeAssignedToMe, ScopeMyGroups:
			// Always applied: never yields to a named set.
			if vis == VisibilityReporter {
				return QueueAny, ""
			}
			if s == ScopeAssignedToMe {
				return QueueAssignedToMe, ""
			}
			return QueueMyGroups, ""
		}
		if isNamed {
			return QueueAny, ""
		}
		switch s {
		case ScopeAll:
			if role != user.RoleAdmin {
				return 0, forbidden
			}
		case ScopeUnassigned:
			if role != user.RoleAdmin && !availOK {
				return 0, forbidden
			}
		}
		if vis == VisibilityReporter {
			return QueueAny, ""
		}
		switch s {
		case ScopeMine:
			if vis == VisibilityScoped {
				return QueueAny, ""
			}
			return QueueMine, ""
		case ScopeUnassigned:
			return QueueUnassigned, ""
		}
		return QueueAny, "" // all, visible
	}

	scopes := []Scope{ScopeMine, ScopeAssignedToMe, ScopeMyGroups, ScopeUnassigned, ScopeAll, ScopeVisible}
	for _, s := range scopes {
		for _, role := range roles {
			for _, vis := range visibilities {
				for _, availOK := range []bool{false, true} {
					for name, f := range named {
						isNamed := name != "none"
						f.Visibility = vis
						wantQ, wantMsg := want(s, role, vis, availOK, isNamed)
						label := string(s) + "/" + string(role) + "/vis" + string(rune('0'+int(vis))) + "/avail=" + map[bool]string{true: "y", false: "n"}[availOK] + "/" + name
						t.Run(label, func(t *testing.T) {
							q, err := ResolveQueue(s, f, role, availOK)
							if wantMsg != "" {
								var fe *FilterError
								require.ErrorAs(t, err, &fe)
								require.Equal(t, FilterForbidden, fe.Kind)
								require.Equal(t, wantMsg, fe.Msg)
								return
							}
							require.NoError(t, err)
							require.Equal(t, wantQ, q)
						})
					}
				}
			}
		}
	}
}

// Rows pinned by hand so the generated table above cannot drift with the
// implementation.
func TestResolveQueue_Pinned(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		name    string
		scope   Scope
		f       Filter
		role    user.Role
		availOK bool
		want    Queue
		wantErr bool
	}{
		{"staff assigned_to_me", ScopeAssignedToMe, Filter{Visibility: VisibilityAll}, user.RoleStaff, false, QueueAssignedToMe, false},
		{"scoped staff assigned_to_me", ScopeAssignedToMe, Filter{Visibility: VisibilityScoped}, user.RoleStaff, false, QueueAssignedToMe, false},
		{"scoped staff my_groups", ScopeMyGroups, Filter{Visibility: VisibilityScoped}, user.RoleStaff, false, QueueMyGroups, false},
		{"my_groups is not replaced by a named group", ScopeMyGroups, Filter{Visibility: VisibilityAll, AssigneeGroupID: &id}, user.RoleStaff, false, QueueMyGroups, false},
		{"assigned_to_me is not replaced by a reporter", ScopeAssignedToMe, Filter{Visibility: VisibilityAll, ReporterID: &id}, user.RoleAdmin, false, QueueAssignedToMe, false},
		{"requester assigned_to_me gets their own", ScopeAssignedToMe, Filter{Visibility: VisibilityReporter}, user.RoleUser, false, QueueAny, false},
		{"mine yields to a named group", ScopeMine, Filter{Visibility: VisibilityAll, AssigneeGroupID: &id}, user.RoleStaff, false, QueueAny, false},
		{"unassigned yields, no refusal", ScopeUnassigned, Filter{Visibility: VisibilityAll, Requester: "ann"}, user.RoleStaff, false, QueueAny, false},
		{"all yields, no refusal", ScopeAll, Filter{Visibility: VisibilityReporter, ReporterID: &id}, user.RoleUser, false, QueueAny, false},
		{"staff unassigned refused", ScopeUnassigned, Filter{Visibility: VisibilityAll}, user.RoleStaff, false, 0, true},
		{"staff unassigned allowed when listed", ScopeUnassigned, Filter{Visibility: VisibilityAll}, user.RoleStaff, true, QueueUnassigned, false},
		{"scoped staff unassigned stays a queue", ScopeUnassigned, Filter{Visibility: VisibilityScoped}, user.RoleStaff, true, QueueUnassigned, false},
		{"admin unassigned without the setting", ScopeUnassigned, Filter{Visibility: VisibilityAll}, user.RoleAdmin, false, QueueUnassigned, false},
		{"requester unassigned refused", ScopeUnassigned, Filter{Visibility: VisibilityReporter}, user.RoleUser, false, 0, true},
		{"staff all refused even when available is ticked", ScopeAll, Filter{Visibility: VisibilityAll}, user.RoleStaff, true, 0, true},
		{"admin all", ScopeAll, Filter{Visibility: VisibilityAll}, user.RoleAdmin, false, QueueAny, false},
		{"scoped staff mine is everything their scope admits", ScopeMine, Filter{Visibility: VisibilityScoped}, user.RoleStaff, false, QueueAny, false},
		{"mine", ScopeMine, Filter{Visibility: VisibilityAll}, user.RoleStaff, false, QueueMine, false},
		{"unknown scope is mine", Scope("bogus"), Filter{Visibility: VisibilityAll}, user.RoleStaff, false, QueueMine, false},
		{"empty scope is mine", Scope(""), Filter{Visibility: VisibilityAll}, user.RoleStaff, false, QueueMine, false},
		{"visible", ScopeVisible, Filter{Visibility: VisibilityScoped}, user.RoleStaff, false, QueueAny, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := ResolveQueue(tc.scope, tc.f, tc.role, tc.availOK)
			if tc.wantErr {
				var fe *FilterError
				require.True(t, errors.As(err, &fe))
				require.Equal(t, FilterForbidden, fe.Kind)
				require.Equal(t, "only admins can use this scope", fe.Msg)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, q)
		})
	}
}
