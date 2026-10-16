package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #444: a staff search covers every ticket the viewer can see, whatever queue
// is selected. scope=visible is that request; scope=mine and scope=unassigned
// stay the narrowed ("Only <queue>") searches.

func searchSubjects(t *testing.T, resp *http.Response) map[string]bool {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out []map[string]any
	decodeJSON(t, resp, &out)
	got := map[string]bool{}
	for _, tk := range out {
		got[tk["subject"].(string)] = true
	}
	return got
}

// newTicketAsAdmin files a ticket and optionally assigns it to a group.
func newTicketAsAdmin(t *testing.T, h *harness, subject string, groupID *uuid.UUID) string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": subject, "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	if groupID != nil {
		resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID,
			map[string]any{"assignee_group_id": groupID.String()})
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	return created.ID
}

func TestSearchVisible_StaffScopeOffFindsEverything(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	other, err := h.groupSvc.Create(ctx, "Other "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	newTicketAsAdmin(t, h, "quokka unassigned", nil)
	newTicketAsAdmin(t, h, "quokka other group", &other.ID)

	got := searchSubjects(t, h.do(t, http.MethodGet, "/api/v1/tickets?scope=visible&q=quokka", nil))
	require.True(t, got["quokka unassigned"])
	require.True(t, got["quokka other group"])

	// The narrowed search (chip on) is unchanged: nothing is assigned to this
	// staff member or their groups.
	got = searchSubjects(t, h.do(t, http.MethodGet, "/api/v1/tickets?scope=mine&q=quokka", nil))
	require.Empty(t, got)
}

func TestSearchVisible_StaffScopeOnStopsAtTheirGroups(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	mine, err := h.groupSvc.Create(ctx, "Mine "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, mine.ID, h.staffID))
	other, err := h.groupSvc.Create(ctx, "Other "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	newTicketAsAdmin(t, h, "wombat in my group", &mine.ID)
	newTicketAsAdmin(t, h, "wombat in other group", &other.ID)
	enableScope(t, h)

	got := searchSubjects(t, h.do(t, http.MethodGet, "/api/v1/tickets?scope=visible&q=wombat", nil))
	require.True(t, got["wombat in my group"])
	require.False(t, got["wombat in other group"],
		"a group-scoped staff member must not find another group's ticket")
}

func TestSearchVisible_StaffScopeOnWithNoGroupsFindsNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	newTicketAsAdmin(t, h, "numbat unassigned", nil)
	enableScope(t, h)

	got := searchSubjects(t, h.do(t, http.MethodGet, "/api/v1/tickets?scope=visible&q=numbat", nil))
	require.Empty(t, got)
}

func TestSearchVisible_AdminFindsEverythingAndNarrowsToQueue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	grp, err := h.groupSvc.Create(ctx, "Grp "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	newTicketAsAdmin(t, h, "bilby unassigned", nil)
	newTicketAsAdmin(t, h, "bilby grouped", &grp.ID)
	enableScope(t, h) // admins are not scoped

	got := searchSubjects(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=visible&q=bilby", nil))
	require.True(t, got["bilby unassigned"])
	require.True(t, got["bilby grouped"])

	// Chip on, queue "unassigned" / "mine".
	got = searchSubjects(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=unassigned&q=bilby", nil))
	require.Equal(t, map[string]bool{"bilby unassigned": true}, got)
	got = searchSubjects(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=mine&q=bilby", nil))
	require.Empty(t, got)
}

func TestSearchVisible_RequesterStillSeesOnlyTheirOwn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "dingo requester's", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	newTicketAsAdmin(t, h, "dingo someone else's", nil)

	got := searchSubjects(t, h.doAsUser(t, http.MethodGet, "/api/v1/tickets?scope=visible&q=dingo", nil))
	require.Equal(t, map[string]bool{"dingo requester's": true}, got)
}

func TestSearchVisible_Paging(t *testing.T) {
	for _, enforced := range []bool{false, true} {
		t.Run(fmt.Sprintf("scope enforced=%v", enforced), func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()

			grp, err := h.groupSvc.Create(ctx, "Paged "+uuid.NewString()[:8], "")
			require.NoError(t, err)
			require.NoError(t, h.groupSvc.AddMember(ctx, grp.ID, h.staffID))
			const total = 12
			for i := 0; i < total; i++ {
				newTicketAsAdmin(t, h, fmt.Sprintf("echidna %02d", i), &grp.ID)
			}
			if enforced {
				enableScope(t, h)
			}

			page := func(offset int) map[string]bool {
				q := url.Values{"scope": {"visible"}, "q": {"echidna"}, "limit": {"5"}, "offset": {fmt.Sprint(offset)}}
				return searchSubjects(t, h.do(t, http.MethodGet, "/api/v1/tickets?"+q.Encode(), nil))
			}
			seen := map[string]bool{}
			for off, want := range map[int]int{0: 5, 5: 5, 10: 2, 15: 0} {
				p := page(off)
				require.Len(t, p, want, "offset %d", off)
				for s := range p {
					require.False(t, seen[s], "%s repeated across pages", s)
					seen[s] = true
				}
			}
			require.Len(t, seen, total)
		})
	}
}
