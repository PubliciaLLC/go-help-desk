package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A role change sticks.
//
// The role was written by its own guarded statement and then written back by
// the profile update in the same request, so promotions and demotions were
// silent no-ops: the response answered 200 carrying the OLD role, and the
// person being "demoted" was signed out for a change that did not happen.
// Nobody could be made an administrator, and nobody could be unmade one.
//
// The existing test read the status code and never read the role back, which
// is exactly how this survived.
func TestUpdateUser_ARoleChangeIsActuallyWritten(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	target, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "promote-me@test.local", DisplayName: "Promote Me",
		Role: user.RoleStaff, Password: "a-real-passphrase",
	})
	require.NoError(t, err)

	admin := loggedInAdmin(t, h)

	for _, want := range []user.Role{user.RoleAdmin, user.RoleUser, user.RoleStaff} {
		t.Run("to "+string(want), func(t *testing.T) {
			res, body := admin.send(t, http.MethodPatch,
				"/api/v1/admin/users/"+target.ID.String(), map[string]any{"role": string(want)})
			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

			// The reply says so...
			var replied struct {
				Role user.Role `json:"role"`
			}
			require.NoError(t, json.Unmarshal(body, &replied))
			require.Equal(t, want, replied.Role,
				"the response carries the old role, which is what made this believable")

			// ...and so does the record.
			stored, err := h.userSvc.GetByIDAdmin(ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, want, stored.Role, "the role change was undone in the same request")
		})
	}
}

// And a rename does not quietly change anybody's role.
func TestUpdateUser_ARenameLeavesTheRoleAlone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	admin := loggedInAdmin(t, h)
	res, body := admin.send(t, http.MethodPatch,
		"/api/v1/admin/users/"+h.staffID.String(), map[string]any{"display_name": "Renamed Staff"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	stored, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
	require.NoError(t, err)
	require.Equal(t, user.RoleStaff, stored.Role)
	require.Equal(t, "Renamed Staff", stored.DisplayName)
}
