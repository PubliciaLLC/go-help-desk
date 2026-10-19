package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Erik (#434): a service user can be renamed, given groups, disabled, and set
// to staff or user (Q2, 2026-10-13: "admin is never allowed"); it is deleted by
// deleting its client. Its address, the admin role and an MFA reset are refused
// by the server, not just hidden by the page: the API is the only enforcement
// a script or a second admin tab respects.
func TestServiceAccount_PatchRefusesEveryOtherField(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want []string // fields the message names
	}{
		{"email", map[string]any{"email": "someone@example.test"}, []string{"email"}},
		{"role to admin", map[string]any{"role": "admin"}, []string{"role"}},
		{"reset_mfa", map[string]any{"reset_mfa": true}, []string{"reset_mfa"}},
		{"email with an allowed field beside it", map[string]any{"email": "x@example.test", "display_name": "Renamed"}, []string{"email"}},
		{"rename and role beside a refused email", map[string]any{"display_name": "Renamed", "role": "user", "email": "x@example.test"}, []string{"email"}},
		{"disable with a refused field beside it", map[string]any{"disabled": true, "role": "admin"}, []string{"role"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			c := h.newServiceClient(t, "Nagios", "tickets:read")
			before, err := h.userSvc.GetByID(context.Background(), c.UserID)
			require.NoError(t, err)

			resp := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/users/"+c.UserID.String(), tc.body)
			var e struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.Equal(t, http.StatusConflict, resp.StatusCode)
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&e))
			require.Equal(t, "service_account", e.Error.Code, "the code the delete refusal uses")
			require.Contains(t, e.Error.Message, "Nagios", "names the client, like the delete refusal")
			for _, f := range tc.want {
				require.Contains(t, e.Error.Message, f)
			}

			// Refused whole: nothing in the request was applied, the allowed
			// fields beside the refused one included.
			after, err := h.userSvc.GetByID(context.Background(), c.UserID)
			require.NoError(t, err)
			require.Equal(t, before.Email, after.Email)
			require.Equal(t, before.Role, after.Role)
			require.Equal(t, before.DisplayName, after.DisplayName)
			require.False(t, after.Disabled, "the disable beside the refused field was not applied")
		})
	}
}

func TestServiceAccount_PatchAllowsStaffAndUserRoleButNeverAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	base := "/api/v1/admin/users/" + c.UserID.String()

	for _, role := range []string{"user", "staff"} {
		require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
			map[string]any{"role": role}).StatusCode, role)
		u, err := h.userSvc.GetByID(context.Background(), c.UserID)
		require.NoError(t, err)
		require.Equal(t, role, string(u.Role))
	}
	// reset_mfa:false asks for nothing, so it is not a refusal; true is.
	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
		map[string]any{"reset_mfa": false, "display_name": "Nagios 2"}).StatusCode)
}

func TestServiceAccount_PatchStillAllowsNameAndDisabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	base := "/api/v1/admin/users/" + c.UserID.String()

	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
		map[string]any{"display_name": "Nagios (prod)", "disabled": true}).StatusCode)
	u, err := h.userSvc.GetByID(context.Background(), c.UserID)
	require.NoError(t, err)
	require.Equal(t, "Nagios (prod)", u.DisplayName)
	require.True(t, u.Disabled)

	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, base,
		map[string]any{"disabled": false}).StatusCode)
	// The same body on a person is not touched by any of this.
	person := "/api/v1/admin/users/" + h.userID.String()
	require.Equal(t, http.StatusOK, h.doAsAdminSession(t, http.MethodPatch, person,
		map[string]any{"email": "renamed.user@test.local", "role": "staff"}).StatusCode)
}

// The other admin routes that act on a user, for a service user. The ones that
// would give it a way to sign in, or hand it to a person, are refused already;
// this pins that each answers the same 409/400 and changes nothing.
func TestServiceAccount_OtherAdminRoutesRefuseIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")
	id := c.UserID.String()

	resp := h.doAsAdminSession(t, http.MethodPost, "/api/v1/admin/users/"+id+"/password",
		map[string]any{"new_password": "a-real-passphrase-123"})
	require.Equal(t, http.StatusConflict, resp.StatusCode, "password set")

	resp = h.doAsAdminSession(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]any{"name": "k", "scopes": []string{"tickets:read"}, "user_id": id, "expires_at": inYears(1)})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "a key on its behalf")

	resp = h.doAsAdminSession(t, http.MethodDelete, "/api/v1/admin/users/"+id, nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode, "delete")
}
