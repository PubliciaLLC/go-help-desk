package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Who may own an API key (#450, decisions of 2026-10-16): staff and
// administrators. Never a requester, never a service account. An
// administrator issues for a staff member or for themselves, not for another
// administrator.

func TestAdminIssueKey_OnlyStaffAndTheAdministratorThemselvesCanOwnOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	other, err := h.userSvc.Create(context.Background(), user.CreateUserInput{
		Email: "admin2@test.local", DisplayName: "Admin Two", Role: user.RoleAdmin, Password: "password",
	})
	require.NoError(t, err)
	svc := h.newServiceClient(t, "svc", "tickets:read")

	cases := []struct {
		name  string
		owner uuid.UUID
		want  int
		code  string
	}{
		{"a staff member", h.staffID, http.StatusCreated, ""},
		{"themselves", h.adminID, http.StatusCreated, ""},
		{"a requester", h.userID, http.StatusBadRequest, "requester_cannot_own_key"},
		{"another administrator", other.ID, http.StatusBadRequest, "cannot_issue_for_administrator"},
		{"a service account", svc.UserID, http.StatusBadRequest, "service_account_cannot_own_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, b := h.issueFor(t, admin, tc.owner, "tickets:read")
			require.Equal(t, tc.want, res.StatusCode, "body: %s", b)
			if tc.code != "" {
				require.Equal(t, tc.code, errCode(t, b))
				keys, err := h.authStore.ListByUser(context.Background(), tc.owner)
				require.NoError(t, err)
				for _, k := range keys {
					require.NotEqual(t, "issued", k.Name, "nothing was issued")
				}
			}
		})
	}
}

// The owner is judged before the scopes: a requester asking for a scope that
// does not exist is told they cannot have a key at all.
func TestAdminIssueKey_TheOwnerIsRefusedBeforeTheScopes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res, b := h.issueFor(t, h.session(t, asAdmin), h.userID, "sprockets:read")
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "requester_cannot_own_key", errCode(t, b))
}

// GET /admin/scopes?role=staff is the picker's list for the account a key is
// issued for (#450).
func TestAdminScopes_CanBeNarrowedToARole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)
	list := func(q string) (int, []string) {
		res := h.doSession(t, admin, http.MethodGet, "/api/v1/admin/scopes"+q, nil)
		b := readAll(t, res)
		var out []struct{ Scope string }
		_ = json.Unmarshal(b, &out)
		var s []string
		for _, o := range out {
			s = append(s, o.Scope)
		}
		return res.StatusCode, s
	}
	code, all := list("")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, all, "users:write")

	code, staff := list("?role=staff")
	require.Equal(t, http.StatusOK, code)
	require.ElementsMatch(t, []string{"tickets:read", "tickets:write", "groups:read", "audit:read"}, staff)

	code, adm := list("?role=admin")
	require.Equal(t, http.StatusOK, code)
	require.ElementsMatch(t, all, adm)

	// Nobody who can only be refused a key is offered a list.
	for _, role := range []string{"user", "nonsense"} {
		code, _ := list("?role=" + role)
		require.Equal(t, http.StatusBadRequest, code, role)
	}
}
