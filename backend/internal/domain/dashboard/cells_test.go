package dashboard

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// cellParams is the DESIGN.md queue-strip table: each key's parameter string,
// written out here independently of Cells.
var cellParams = map[string]string{
	"assigned_to_me":    "scope=assigned_to_me&include_closed=false",
	"my_groups":         "scope=my_groups&include_closed=false",
	"sla_breached":      "scope=assigned_to_me&sla=breached&include_closed=false",
	"sla_at_risk":       "scope=assigned_to_me&sla=at_risk&include_closed=false",
	"awaiting_my_reply": "scope=assigned_to_me&awaiting_reply=true&include_closed=false",
	"my_tickets":        "scope=mine&include_closed=false",
}

func TestCells(t *testing.T) {
	cases := []struct {
		name  string
		role  user.Role
		slaOn bool
		want  []string
	}{
		{"staff, sla on", user.RoleStaff, true,
			[]string{"assigned_to_me", "my_groups", "sla_breached", "sla_at_risk", "awaiting_my_reply"}},
		{"staff, sla off", user.RoleStaff, false,
			[]string{"assigned_to_me", "my_groups", "awaiting_my_reply"}},
		{"admin, sla on", user.RoleAdmin, true,
			[]string{"assigned_to_me", "my_groups", "sla_breached", "sla_at_risk", "awaiting_my_reply"}},
		{"admin, sla off", user.RoleAdmin, false,
			[]string{"assigned_to_me", "my_groups", "awaiting_my_reply"}},
		{"requester, sla on", user.RoleUser, true, []string{"my_tickets"}},
		{"requester, sla off", user.RoleUser, false, []string{"my_tickets"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cells := Cells(tc.role, tc.slaOn)

			keys := make([]string, 0, len(cells))
			for _, c := range cells {
				keys = append(keys, c.Key)
			}
			require.Equal(t, tc.want, keys)

			seen := map[string]bool{}
			for _, c := range cells {
				require.False(t, seen[c.Key], "duplicate key %q", c.Key)
				seen[c.Key] = true
			}

			for _, c := range cells {
				require.True(t, c.Filter.ExcludeClosed, "cell %q must exclude closed", c.Key)

				q, err := url.ParseQuery(cellParams[c.Key])
				require.NoError(t, err)

				wantScope, ok := ticket.ParseScope(q.Get("scope"))
				require.True(t, ok)
				require.Equal(t, wantScope, c.Scope, "scope of %q", c.Key)

				wantFilter, err := ticket.ParseListFilter(q)
				require.NoError(t, err)
				require.Equal(t, wantFilter, c.Filter, "filter of %q", c.Key)

				// The queue the handler resolves from the cell is the one the
				// parameter string resolves to.
				got, err := ticket.ResolveQueue(c.Scope, c.Filter, tc.role, true)
				require.NoError(t, err)
				want, err := ticket.ResolveQueue(wantScope, wantFilter, tc.role, true)
				require.NoError(t, err)
				require.Equal(t, want, got, "queue of %q", c.Key)
			}
		})
	}
}
