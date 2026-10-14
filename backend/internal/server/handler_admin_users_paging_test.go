package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// #449: GET /admin/users returned the newest 200 accounts and ignored every
// query parameter. Paging, search and the role filter are opt-in through
// ?page=, so a client that does not send it must see exactly what it saw
// before.

// seedBulkUsers inserts n reporting users straight into the table. Going
// through user.Service would cost a bcrypt hash apiece, and what is under test
// here is the list, not account creation.
func seedBulkUsers(t *testing.T, h *harness, n int) {
	t.Helper()
	_, err := h.tx.Exec(`
		INSERT INTO users (id, email, display_name, role, created_at, updated_at)
		SELECT gen_random_uuid(), 'bulk' || g || '@bulk.local', 'Bulk ' || g, 'user',
		       now() - make_interval(secs => g), now()
		FROM generate_series(1, $1::int) g`, n)
	require.NoError(t, err)
}

type listedUsers struct {
	status int
	total  string
	users  []map[string]any
	raw    string
}

func listUsersAt(t *testing.T, h *harness, query string) listedUsers {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users"+query, nil)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	out := listedUsers{status: res.StatusCode, total: res.Header.Get("X-Total-Count"), raw: string(b)}
	if res.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(b, &out.users), "body must stay a bare array: %s", b)
	}
	return out
}

func TestAdminUsers_UnpagedRequestIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedBulkUsers(t, h, 205) // plus the three seeded accounts

	// Parameters that did nothing before still do nothing: the frontend sent
	// limit and offset, and the 1.2 docs promised ?role=. None of them may
	// start to bite on a client that has not asked for a page.
	for _, q := range []string{"", "?limit=5", "?limit=200&offset=0", "?offset=10", "?role=admin", "?q=Bulk%201"} {
		got := listUsersAt(t, h, q)
		require.Equal(t, http.StatusOK, got.status, q)
		require.Len(t, got.users, 200, "unpaged keeps the 200 cap (%q)", q)
		require.Empty(t, got.total, "no total header outside paged mode (%q)", q)
	}
}

func TestAdminUsers_PagedModeServesFifty(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedBulkUsers(t, h, 205) // 208 accounts in all

	first := listUsersAt(t, h, "?page=1")
	require.Equal(t, http.StatusOK, first.status)
	require.Len(t, first.users, 50)
	require.Equal(t, "208", first.total)

	// The cap lifts for a client that asks for later pages.
	last := listUsersAt(t, h, "?page=5")
	require.Len(t, last.users, 8)
	require.Equal(t, "208", last.total)

	past := listUsersAt(t, h, "?page=6")
	require.Equal(t, http.StatusOK, past.status)
	require.Empty(t, past.users)
	require.Equal(t, "208", past.total)

	// Pages tile the list: no account twice, none dropped.
	seen := map[string]bool{}
	for p := 1; p <= 5; p++ {
		for _, u := range listUsersAt(t, h, fmt.Sprintf("?page=%d", p)).users {
			id := u["id"].(string)
			require.False(t, seen[id], "account %s appeared on two pages", id)
			seen[id] = true
		}
	}
	require.Len(t, seen, 208)
}

func TestAdminUsers_PerPageIsClamped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedBulkUsers(t, h, 250)

	require.Len(t, listUsersAt(t, h, "?page=1&per_page=10").users, 10)
	require.Len(t, listUsersAt(t, h, "?page=1&per_page=9999").users, 200)
}

func TestAdminUsers_SearchMatchesNameOrEmailIgnoringCase(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	_, err := h.userSvc.Create(ctx, userInput("grace@hopper.test", "Grace Hopper"))
	require.NoError(t, err)

	byName := listUsersAt(t, h, "?page=1&q=grace%20HOP")
	require.Len(t, byName.users, 1)
	require.Equal(t, "grace@hopper.test", byName.users[0]["email"])
	require.Equal(t, "1", byName.total)

	byEmail := listUsersAt(t, h, "?page=1&q=@HOPPER.test")
	require.Len(t, byEmail.users, 1)

	// % and _ are characters to find, not wildcards.
	require.Empty(t, listUsersAt(t, h, "?page=1&q=%25").users)
	require.Empty(t, listUsersAt(t, h, "?page=1&q=gr_ce").users)
}

func TestAdminUsers_RoleFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedBulkUsers(t, h, 3)

	staff := listUsersAt(t, h, "?page=1&role=staff")
	require.Len(t, staff.users, 1)
	require.Equal(t, "staff", staff.users[0]["role"])
	require.Equal(t, "1", staff.total)

	require.Len(t, listUsersAt(t, h, "?page=1&role=user").users, 4) // seeded reporter + 3

	// Filter and search together.
	both := listUsersAt(t, h, "?page=1&role=user&q=bulk2")
	require.Len(t, both.users, 1)
}

func TestAdminUsers_PagedModeRefusesBadParameters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?page=1&per_page=0", "?page=1&per_page=x", "?page=1&role=owner"} {
		got := listUsersAt(t, h, q)
		require.Equal(t, http.StatusBadRequest, got.status, "%s: %s", q, got.raw)
	}
}

func TestAdminUsers_DisabledAccountsStayInPagedList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.userSvc.Disable(ctx, h.staffID))

	got := listUsersAt(t, h, "?page=1&role=staff")
	require.Len(t, got.users, 1)
	require.Equal(t, true, got.users[0]["disabled"])
}
