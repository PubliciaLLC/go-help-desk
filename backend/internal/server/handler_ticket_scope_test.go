package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// Scope enforcement is decided by a SQL predicate joining group_members and
// group_scopes, so these run against real Postgres. A fake would assert that
// a Go reimplementation agrees with itself.
//
// The rule under test is the union from DESIGN.md: a staff member sees a ticket
// they reported, one assigned to them, one assigned to a group they belong to,
// or one whose Category/Type a group of theirs covers.

func enableScope(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyTicketScopeEnforced, true))
}

// TestTicketScope_OffByDefault pins the upgrade path. Every release before this
// let staff see every ticket; switching that on silently would hide tickets
// people are mid-conversation on.
func TestTicketScope_OffByDefault(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	require.False(t, h.adminSvc.TicketScopeEnforced(context.Background()),
		"scope enforcement must default to off")

	// A ticket the staff user has nothing to do with.
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Admin's own ticket",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	// Staff can read it, because enforcement is off.
	got := h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusOK, got.StatusCode,
		"with enforcement off, staff retain the access they have always had")
}

// TestTicketScope_HidesOutOfScopeTicket is the actual exposure being closed:
// before this, any staff member could read any ticket by ID.
func TestTicketScope_HidesOutOfScopeTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Not the staff user's business",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	enableScope(t, h)

	got := h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusForbidden, got.StatusCode,
		"a staff member with no group covering this ticket must not read it")

	// The admin still can.
	got = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusOK, got.StatusCode, "admins are never scoped")
}

// TestTicketScope_CategoryScopeGrantsAccess covers the queue case, and is why
// the union reading matters: nobody has been assigned this ticket yet.
func TestTicketScope_CategoryScopeGrantsAccess(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Unassigned, in the staff user's category",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	enableScope(t, h)

	// Out of scope to begin with.
	require.Equal(t, http.StatusForbidden,
		h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil).StatusCode)

	// Put the staff user in a group scoped to that category.
	g, err := h.groupSvc.Create(ctx, "Tier 1", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: h.catID}))

	require.Equal(t, http.StatusOK,
		h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil).StatusCode,
		"a category-level scope must grant access to an unassigned ticket in it")
}

// TestTicketScope_AssignmentGrantsAccessOutsideScope is the other half of the
// union: assigned to you, but in a category none of your groups cover.
func TestTicketScope_AssignmentGrantsAccessOutsideScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Handed to the staff user directly",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	enableScope(t, h)
	require.Equal(t, http.StatusForbidden,
		h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil).StatusCode)

	// Assign it to them; no group, no scope.
	patch := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
		"assignee_user_id": h.staffID.String(),
	})
	require.Equal(t, http.StatusOK, patch.StatusCode)

	require.Equal(t, http.StatusOK,
		h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil).StatusCode,
		"an assigned ticket is visible regardless of scope")
}

// TestTicketScope_ListIncludesUnassignedInScope checks the SQL predicate, not
// just the per-ticket rule — the list is a different code path and paginates,
// so it cannot be filtered in Go.
func TestTicketScope_ListIncludesUnassignedInScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Queue item awaiting pickup",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	enableScope(t, h)

	// Before joining a scoped group, the staff list is empty.
	var before []map[string]any
	decodeJSON(t, h.do(t, http.MethodGet, "/api/v1/tickets", nil), &before)
	require.Empty(t, before, "nothing assigned and nothing in scope means an empty queue")

	g, err := h.groupSvc.Create(ctx, "Tier 1", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: h.catID}))

	var after []map[string]any
	decodeJSON(t, h.do(t, http.MethodGet, "/api/v1/tickets", nil), &after)
	require.Len(t, after, 1, "the unassigned in-scope ticket must appear in the queue")
	require.Equal(t, "Queue item awaiting pickup", after[0]["subject"])
}

// TestTicketScope_ReportingUserUnaffected pins that switching this on does not
// change what a reporting user sees — they were already restricted.
func TestTicketScope_ReportingUserUnaffected(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "Someone else's",
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	for _, enforced := range []bool{false, true} {
		if enforced {
			enableScope(t, h)
		}
		got := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
		require.Equal(t, http.StatusForbidden, got.StatusCode,
			"a reporting user never sees another's ticket (enforced=%v)", enforced)
	}
}

var _ = uuid.Nil

// The group filter was a filter, not a boundary.
//
// requireTicketAccess refuses an out-of-scope ticket by id, and the default
// listing omits it — but ?assignee_group_id= refused only RoleUser and then ran
// an unscoped query. Group ids are handed to every staff member by GET /groups,
// so naming a group you do not belong to returned its tickets. An OAuth client
// acts as staff and belongs to no group at all, which made it every ticket
// assigned to any group.
func TestTicketScope_GroupFilterRespectsScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// A group the staff user is NOT in, holding a ticket.
	grp, err := h.groupSvc.Create(ctx, "Legal", "")
	require.NoError(t, err)

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Sensitive legal matter", "category_id": h.catID.String(), "priority": "high",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID,
		map[string]any{"assignee_group_id": grp.ID.String()})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	enableScope(t, h)

	// The boundary that already worked, as the control.
	got := h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusForbidden, got.StatusCode,
		"precondition: the ticket is out of scope by id")

	// The same ticket, asked for by group.
	got = h.do(t, http.MethodGet, "/api/v1/tickets?assignee_group_id="+grp.ID.String(), nil)
	require.Equal(t, http.StatusForbidden, got.StatusCode,
		"naming a group you are not in must not hand over its tickets")

	// And with a search term, which took a different query.
	got = h.do(t, http.MethodGet,
		"/api/v1/tickets?assignee_group_id="+grp.ID.String()+"&q=Sensitive", nil)
	require.Equal(t, http.StatusForbidden, got.StatusCode,
		"the search variant must be scoped too")
}

// A staff member IN the group must still be able to filter by it, and an admin
// is not scoped at all — a guard that refused everyone would pass the test above.
func TestTicketScope_GroupFilterStillWorksForMembers(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	grp, err := h.groupSvc.Create(ctx, "Support", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, grp.ID, h.staffID))

	enableScope(t, h)

	got := h.do(t, http.MethodGet, "/api/v1/tickets?assignee_group_id="+grp.ID.String(), nil)
	require.Equal(t, http.StatusOK, got.StatusCode,
		"a member must still be able to filter by their own group")

	got = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?assignee_group_id="+grp.ID.String(), nil)
	require.Equal(t, http.StatusOK, got.StatusCode,
		"admins are not scoped")
}
