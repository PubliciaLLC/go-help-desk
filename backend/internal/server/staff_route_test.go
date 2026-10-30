package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The staff list is served where the frontend asks for it.
//
// It was first registered on the ticket router, which is mounted at
// /api/v1/tickets — so it answered at /api/v1/tickets/staff while the page
// asked for /api/v1/staff and got a 404. Every assignee picker was empty and
// every assigned ticket showed no name, for administrators as well as staff:
// worse than the problem the endpoint was added to fix, because
// administrators had a working picker before it.
//
// Nothing caught it. No test hit the route, no test asserted the URL, and
// tsc, eslint and both suites were green. A 404 on a query whose failure the
// page handles quietly is invisible to everything except somebody using it.
func TestStaffList_IsServedAtTheDocumentedPath(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.do(t, http.MethodGet, "/api/v1/staff", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"/api/v1/staff is what frontend/src/api/admin.ts asks for")

	var staff []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Assignable  bool   `json:"assignable"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&staff))
	require.NotEmpty(t, staff, "the harness seeds staff and an admin")
	for _, s := range staff {
		require.NotEmpty(t, s.DisplayName)
		require.NotEmpty(t, s.ID)
	}

	// And nowhere else, so the two cannot drift apart again.
	stale := h.do(t, http.MethodGet, "/api/v1/tickets/staff", nil)
	defer stale.Body.Close()
	require.NotEqual(t, http.StatusOK, stale.StatusCode,
		"the route is still mounted under /tickets as well, which is how it went wrong")
}

// Only the people who assign work can read it, and it says nothing else about
// them.
func TestStaffList_IsNarrowAndStaffOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("a reporting user cannot read it", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodGet, "/api/v1/staff", nil)
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("it carries a name, an id, and whether they can take work", func(t *testing.T) {
		res := h.do(t, http.MethodGet, "/api/v1/staff", nil)
		defer res.Body.Close()

		var raw []map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&raw))
		require.NotEmpty(t, raw)
		for _, entry := range raw {
			require.Len(t, entry, 3,
				"an email address, a role or a login state is the administrator's view: %v", entry)
			require.Contains(t, entry, "id")
			require.Contains(t, entry, "display_name")
			require.Contains(t, entry, "assignable")
		}
	})
}
