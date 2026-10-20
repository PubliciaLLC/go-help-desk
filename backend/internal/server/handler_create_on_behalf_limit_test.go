package server_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #444, ruling 2: a typed address with no account is mailed on the strength of
// a staff member's say-so alone, so the number of them is capped per staff
// member: 40 tickets an hour, then 429. Only that path counts. A picked user or
// a typed address that belongs to an account is a person the help desk already
// knows, and is not what the cap is for.

const onBehalfMailBudget = 40

// typedNew files a ticket for the i-th address that has no account, as the
// given caller, and returns the response.
func typedNew(t *testing.T, h *harness, as string, i int) *http.Response {
	t.Helper()
	return h.postOnBehalf(t, as, map[string]any{
		"reporter_email": fmt.Sprintf("walk-in-%d@example.test", i), "guest_name": "Ada",
	})
}

func newLimitHarness(t *testing.T) (*harness, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
	return h, cleanup
}

func TestCreateTicket_OnBehalf_TypedAddressIsCappedPerHour(t *testing.T) {
	h, cleanup := newLimitHarness(t)
	defer cleanup()

	for i := 0; i < onBehalfMailBudget; i++ {
		res := typedNew(t, h, "staff", i)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
	}
	before := ticketCount(t, h)

	res := typedNew(t, h, "staff", onBehalfMailBudget)
	body := readBody(t, res)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, body)
	require.Contains(t, body, "rate_limited")
	wait, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err, "a 429 says when to come back")
	require.GreaterOrEqual(t, wait, 1)
	require.LessOrEqual(t, wait, 3600, "never longer than the hour")
	require.Equal(t, before, ticketCount(t, h), "the refused one created nothing")
}

// A picked user is not an address probe: it neither spends the budget nor is
// stopped by it. (A typed address spends a unit whether or not it has an
// account: see TestOnBehalf_AccountAddressSpendsAUnit.)
func TestCreateTicket_OnBehalf_PickedUsersDoNotSpendTheBudget(t *testing.T) {
	h, cleanup := newLimitHarness(t)
	defer cleanup()
	picked := map[string]any{"reporter_user_id": h.userID.String()}

	for i := 0; i < 10; i++ {
		res := h.postOnBehalf(t, "staff", picked)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
	}
	// If those ten had counted, the last of these would be refused.
	for i := 0; i < onBehalfMailBudget; i++ {
		res := typedNew(t, h, "staff", i)
		require.Equal(t, http.StatusCreated, res.StatusCode, "typed #%d: %s", i, readBody(t, res))
		res.Body.Close()
	}
	require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "staff", onBehalfMailBudget).StatusCode)

	// And a spent budget does not stop them.
	res := h.postOnBehalf(t, "staff", picked)
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	res.Body.Close()
}

// The budget belongs to the staff member: another one is unaffected.
func TestCreateTicket_OnBehalf_BudgetIsPerStaffMember(t *testing.T) {
	h, cleanup := newLimitHarness(t)
	defer cleanup()

	for i := 0; i < onBehalfMailBudget; i++ {
		res := typedNew(t, h, "staff", i)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
	}
	require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "staff", 1000).StatusCode)

	res := typedNew(t, h, "admin", 1001)
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	res.Body.Close()
}

// A machine credential is its owner: what it files comes out of the same
// budget as the owner's own session, so a key is no way round the cap, and a
// key is not a budget of its own.
func TestCreateTicket_OnBehalf_BudgetFollowsTheCredentialsUser(t *testing.T) {
	h, cleanup := newLimitHarness(t)
	defer cleanup()
	key := mintKey(t, h, []string{"tickets:write", "tickets:read"})

	for i := 0; i < onBehalfMailBudget; i++ {
		var res *http.Response
		if i%2 == 0 {
			res = typedNew(t, h, "admin", i)
		} else {
			res = withKey(t, h, key, http.MethodPost, "/api/v1/tickets", map[string]any{
				"subject": "Phone-in", "category_id": h.catID.String(),
				"reporter_email": fmt.Sprintf("walk-in-%d@example.test", i), "guest_name": "Ada",
			})
		}
		require.Equal(t, http.StatusCreated, res.StatusCode, "#%d: %s", i, readBody(t, res))
		res.Body.Close()
	}

	require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "admin", 1000).StatusCode, "the session is out")
	res := withKey(t, h, key, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Phone-in", "category_id": h.catID.String(),
		"reporter_email": "walk-in-1001@example.test", "guest_name": "Ada",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "and so is the key")
}

// A request refused by validation spends nothing: it taught the caller nothing
// about any address, so 40 bad requests must not lock a member of staff out.
// Each row is one way of being refused before the address is looked at. The
// refusals come first and there are more of them than the budget; then a full
// budget of valid requests must still all succeed and the next one be the 429.
//
// These rows pin where the spend sits: put it ahead of any of those checks and
// they go red. Once a unit IS taken it is never refunded, and guest submission
// being off is deliberately not a row (see
// TestOnBehalf_GuestSubmissionOff_ProbesAreCappedAt40).
func TestCreateTicket_OnBehalf_RefusalsBeforeTheUnitSpendNothing(t *testing.T) {
	type row struct {
		name  string
		setup func(t *testing.T, h *harness) // before the refusals
		bad   func(i int) map[string]any     // the refused request's fields
		after func(t *testing.T, h *harness) // before the valid ones
	}
	typed := func(i int, extra map[string]any) map[string]any {
		m := map[string]any{"reporter_email": fmt.Sprintf("walk-in-%d@example.test", i), "guest_name": "Ada"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	var tree archivedTree
	buildTree := func(archive bool) func(t *testing.T, h *harness) {
		return func(t *testing.T, h *harness) {
			tree = buildArchivedTree(t, h)
			if archive {
				tree.archive(t, h)
			}
		}
	}

	rows := []row{
		{name: "no name",
			bad: func(i int) map[string]any { return typed(i, map[string]any{"guest_name": "   "}) }},
		{name: "invalid email",
			bad: func(i int) map[string]any {
				return map[string]any{"reporter_email": "not-an-address", "guest_name": "Ada"}
			}},
		{name: "subject too long",
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{"subject": strings.Repeat("s", ticket.MaxSubjectLength+1)})
			}},
		{name: "description too long",
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{"description": strings.Repeat("d", ticket.MaxDescriptionLength+1)})
			}},
		{name: "bad priority",
			bad: func(i int) map[string]any { return typed(i, map[string]any{"priority": "urgent-ish"}) }},
		{name: "unknown category",
			bad: func(i int) map[string]any { return typed(i, map[string]any{"category_id": uuid.NewString()}) }},
		{name: "archived category", setup: buildTree(true),
			bad: func(i int) map[string]any { return typed(i, map[string]any{"category_id": tree.catArchived.String()}) }},
		{name: "archived type", setup: buildTree(true),
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{"category_id": tree.catOpen.String(), "type_id": tree.typeArch.String()})
			}},
		{name: "archived item", setup: buildTree(true),
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{
					"category_id": tree.catOpen.String(), "type_id": tree.typeOpen.String(), "item_id": tree.itemArch.String()})
			}},
		{name: "type from another category", setup: buildTree(false),
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{"category_id": tree.catOpen.String(), "type_id": tree.typeUnderArc.String()})
			}},
		{name: "item not under the type", setup: buildTree(false),
			bad: func(i int) map[string]any {
				return typed(i, map[string]any{
					"category_id": tree.catOpen.String(), "type_id": tree.typeOpen.String(), "item_id": tree.itemUnderTA.String()})
			}},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newLimitHarness(t)
			defer cleanup()
			if tc.setup != nil {
				tc.setup(t, h)
			}

			for i := 0; i < onBehalfMailBudget+5; i++ {
				res := h.postOnBehalf(t, "staff", tc.bad(i))
				require.Equal(t, http.StatusBadRequest, res.StatusCode, "refused #%d: %s", i, readBody(t, res))
				res.Body.Close()
			}
			require.Equal(t, 0, ticketCount(t, h), "none of those filed anything")
			if tc.after != nil {
				tc.after(t, h)
			}

			for i := 0; i < onBehalfMailBudget; i++ {
				res := typedNew(t, h, "staff", i)
				require.Equal(t, http.StatusCreated, res.StatusCode, "valid #%d: %s", i, readBody(t, res))
				res.Body.Close()
			}
			require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "staff", onBehalfMailBudget).StatusCode,
				"and the forty-first filed ticket is the one that is refused")
		})
	}
}
