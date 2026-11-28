package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// requester_search:read is the free-text requester search permission. It can be
// granted wherever a credential is created, to staff and administrators, and
// to nobody else. The scope check on the parameter itself belongs to the
// handler (see parameter_scopes_test.go).

func TestRequesterSearchScope_KeysOwnedByStaffAndAdminsMayHoldIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	cases := []struct {
		name  string
		owner string
		want  int
		code  string
	}{
		{"a staff-owned key", "staff", http.StatusCreated, ""},
		{"an administrator-owned key", "admin", http.StatusCreated, ""},
		// A requester never owns a key (#450), so the owner check answers first.
		{"a requester-owned key", "user", http.StatusBadRequest, "requester_cannot_own_key"},
	}
	owners := map[string]uuid.UUID{
		"staff": h.staffID, "admin": h.adminID, "user": h.userID,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doSession(t, admin, http.MethodPost, adminKeys, map[string]any{
				"name": "rs-" + tc.owner, "scopes": []string{"requester_search:read"},
				"user_id": owners[tc.owner].String(), "expires_at": inYears(1),
			})
			b := readAll(t, res)
			require.Equal(t, tc.want, res.StatusCode, "body: %s", b)
			if tc.code != "" {
				require.Equal(t, tc.code, errCode(t, b))
			}
		})
	}
}

func TestRequesterSearchScope_StaffMayIssueThemselvesAKeyWithIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)

	res := h.doSession(t, staff, http.MethodPost, meKeys, map[string]any{
		"name": "rs", "scopes": []string{"tickets:read", "requester_search:read"}, "expires_at": inYears(1),
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
}

// There is no write action. It is refused like any unknown scope, at every
// place a credential is made.
func TestRequesterSearchScope_WriteIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	res := h.doSession(t, admin, http.MethodPost, adminKeys, map[string]any{
		"name": "rs-write", "scopes": []string{"requester_search:write"}, "expires_at": inYears(1),
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
	require.Equal(t, "invalid_scope", errCode(t, b))

	res = h.createCredential(t, "/api/v1/admin/oauth-clients",
		map[string]any{"name": "rs-write", "scopes": []string{"requester_search:write"}})
	b = readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
	require.Equal(t, "invalid_scope", errCode(t, b))
}

// An OAuth client's service user is staff, so the scope may be granted to it.
func TestRequesterSearchScope_AnOAuthClientMayHoldIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.createCredential(t, "/api/v1/admin/oauth-clients",
		map[string]any{"name": "rs", "scopes": []string{"tickets:read", "requester_search:read"}})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
}
