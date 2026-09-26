package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Changing a password takes the current one.
//
// It took a session cookie and nothing else. So anyone who got hold of a
// session — a shared machine left unlocked, a stolen cookie — could set a new
// password, and because the change also revokes every other session, the
// owner was locked out of their own account permanently. A borrowed session
// became a taken account.
//
// The frontend's API client has sent current_password since it was written.
// Nothing read it.
func TestChangePassword_RequiresTheCurrentPassword(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "no current password at all",
			body: map[string]any{"new_password": "a-much-better-password"},
			want: http.StatusUnauthorized,
		},
		{
			name: "the wrong current password",
			body: map[string]any{"current_password": "not-it", "new_password": "a-much-better-password"},
			want: http.StatusUnauthorized,
		},
		{
			name: "the right one",
			body: map[string]any{"current_password": "password", "new_password": "a-much-better-password"},
			want: http.StatusNoContent,
		},
		{
			// The original field name, still honoured so an API consumer
			// using it is not broken by the rename — but the current password
			// is required either way, which is the part that was missing.
			name: "the old field name, with the current password",
			body: map[string]any{"current_password": "password", "password": "a-much-better-password"},
			want: http.StatusNoContent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			sess := loggedIn(t, h)
			res, body := sess.send(t, http.MethodPatch, "/api/v1/me/password", tc.body)
			require.Equal(t, tc.want, res.StatusCode, "body: %s", body)
		})
	}
}

// The same minimum on every path that sets a password.
//
// It was three different rules: self-service required eight characters, and
// admin create, admin reset and first-run setup each required one. An
// administrator could create an account with the password "a", or reset one
// to "b", without trying to — and the worst case, an administrator account
// created during setup, was the least guarded of the four.
func TestPassword_TheMinimumAppliesEverywhere(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("admin create", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users", map[string]any{
			"email": "shortpw@test.local", "display_name": "Short", "role": "user",
			"password": "a",
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("admin reset", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPost,
			"/api/v1/admin/users/"+h.userID.String()+"/password", map[string]any{"password": "b"})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})
}
