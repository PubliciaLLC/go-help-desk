package server_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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

// Only an address that would be mailed fresh counts. A picked user and a typed
// address that matches an account neither spend the budget nor are stopped by
// it.
func TestCreateTicket_OnBehalf_AccountsDoNotSpendTheBudget(t *testing.T) {
	h, cleanup := newLimitHarness(t)
	defer cleanup()
	picked := map[string]any{"reporter_user_id": h.userID.String()}
	matching := map[string]any{"reporter_email": "user@test.local"}

	for i := 0; i < 5; i++ {
		for _, extra := range []map[string]any{picked, matching} {
			res := h.postOnBehalf(t, "staff", extra)
			require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
			res.Body.Close()
		}
	}
	// If those ten had counted, the last of these would be refused.
	for i := 0; i < onBehalfMailBudget; i++ {
		res := typedNew(t, h, "staff", i)
		require.Equal(t, http.StatusCreated, res.StatusCode, "typed #%d: %s", i, readBody(t, res))
		res.Body.Close()
	}
	require.Equal(t, http.StatusTooManyRequests, typedNew(t, h, "staff", onBehalfMailBudget).StatusCode)

	// And a spent budget does not stop them.
	for _, extra := range []map[string]any{picked, matching} {
		res := h.postOnBehalf(t, "staff", extra)
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		res.Body.Close()
	}
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
