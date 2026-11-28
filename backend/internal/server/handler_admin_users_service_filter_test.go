package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Users page's "service accounts" filter is server-side (Erik, 1.3.0): in
// paged mode a client-side filter only ever saw one page. ?service_account=true
// is only service users, false only people, absent everyone; the total counts
// the filter. Unpaged mode ignores it, like q and role.

func rawErrCode(t *testing.T, raw string) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &e), raw)
	return e.Error.Code
}

func userIDSet(users []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, u := range users {
		out[u["id"].(string)] = true
	}
	return out
}

func TestAdminUsers_ServiceAccountFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	seedBulkUsers(t, h, 3)
	nagios := h.newServiceClient(t, "Nagios", "tickets:read")
	zapier := h.newServiceClient(t, "Zapier", "tickets:read")

	t.Run("true is only service users", func(t *testing.T) {
		got := listUsersAt(t, h, "?page=1&service_account=true")
		require.Equal(t, http.StatusOK, got.status, got.raw)
		require.Len(t, got.users, 2)
		require.Equal(t, "2", got.total)
		for _, u := range got.users {
			require.Equal(t, true, u["service_account"])
		}
		require.Equal(t, map[string]bool{nagios.UserID.String(): true, zapier.UserID.String(): true}, userIDSet(got.users))
	})

	t.Run("false is only people", func(t *testing.T) {
		got := listUsersAt(t, h, "?page=1&service_account=false")
		require.Equal(t, http.StatusOK, got.status, got.raw)
		require.Equal(t, "6", got.total, "three seeded accounts and three bulk users")
		require.Len(t, got.users, 6)
		for _, u := range got.users {
			require.Equal(t, false, u["service_account"])
		}
	})

	t.Run("absent is everyone", func(t *testing.T) {
		got := listUsersAt(t, h, "?page=1")
		require.Equal(t, "8", got.total)
		require.Len(t, got.users, 8)
	})

	t.Run("the total counts the filter, not the page", func(t *testing.T) {
		got := listUsersAt(t, h, "?page=1&per_page=1&service_account=true")
		require.Len(t, got.users, 1)
		require.Equal(t, "2", got.total)
		second := listUsersAt(t, h, "?page=2&per_page=1&service_account=true")
		require.Len(t, second.users, 1)
		require.NotEqual(t, got.users[0]["id"], second.users[0]["id"])
		require.Empty(t, listUsersAt(t, h, "?page=3&per_page=1&service_account=true").users)
	})

	t.Run("combined with q and role", func(t *testing.T) {
		got := listUsersAt(t, h, "?page=1&service_account=true&q=nagios")
		require.Len(t, got.users, 1)
		require.Equal(t, "1", got.total)
		require.Equal(t, nagios.UserID.String(), got.users[0]["id"])

		// q that only people match is empty for service accounts, and the other way round.
		require.Empty(t, listUsersAt(t, h, "?page=1&service_account=true&q=bulk").users)
		require.Len(t, listUsersAt(t, h, "?page=1&service_account=false&q=bulk").users, 3)
		require.Empty(t, listUsersAt(t, h, "?page=1&service_account=false&q=nagios").users)

		// Service users are staff by default; the role filter composes.
		require.Len(t, listUsersAt(t, h, "?page=1&service_account=true&role=staff").users, 2)
		require.Empty(t, listUsersAt(t, h, "?page=1&service_account=true&role=user").users)
		people := listUsersAt(t, h, "?page=1&service_account=false&role=staff")
		require.Len(t, people.users, 1, "the seeded staff member, not the two clients")
		require.Equal(t, "1", people.total)
	})

	t.Run("an empty value is the same as absent", func(t *testing.T) {
		require.Equal(t, "8", listUsersAt(t, h, "?page=1&service_account=").total)
	})
}

func TestAdminUsers_ServiceAccountFilterRefusesBadValues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, v := range []string{"yes", "1", "TRUE", "maybe", "null"} {
		got := listUsersAt(t, h, "?page=1&service_account="+v)
		require.Equal(t, http.StatusBadRequest, got.status, "%s: %s", v, got.raw)
		require.Equal(t, "bad_request", rawErrCode(t, got.raw))
	}
}

// Without ?page= the filter does nothing, exactly as q and role do nothing:
// the response is byte-identical.
func TestAdminUsers_ServiceAccountFilterIsIgnoredUnpaged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.newServiceClient(t, "Nagios", "tickets:read")

	base := listUsersAt(t, h, "")
	require.Equal(t, http.StatusOK, base.status)
	require.Empty(t, base.total)
	for _, q := range []string{"?service_account=true", "?service_account=false", "?service_account=garbage"} {
		got := listUsersAt(t, h, q)
		require.Equal(t, base.raw, got.raw, q)
		require.Empty(t, got.total, q)
	}
}
