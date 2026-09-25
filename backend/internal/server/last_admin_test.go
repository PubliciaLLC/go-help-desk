package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The only administrator cannot be disabled, demoted or deleted.
//
// All three answered success and the next request answered 401. Setup does
// not reopen — that is deliberate, and it is exactly what makes this
// unrecoverable: the instance is left with no administrator and no way to
// make one, short of editing the database by hand.
//
// One request, by somebody who is allowed to make it, and the help desk can
// no longer be administered.
func TestLastAdministrator_CannotBeRemoved(t *testing.T) {
	cases := []struct {
		name   string
		method string
		body   any
	}{
		{"disabled", http.MethodPatch, map[string]any{"disabled": true}},
		{"demoted", http.MethodPatch, map[string]any{"role": "staff"}},
		{"deleted", http.MethodDelete, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			// A signed-in session, not the API key: acting on an
			// administrator is deliberately refused to machine credentials.
			admin := loggedInAdmin(t, h)
			res, body := admin.send(t, tc.method, "/api/v1/admin/users/"+h.adminID.String(), tc.body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"the only administrator was %s, so nobody can administer this instance now: %s",
				tc.name, body)
		})
	}
}

// And with a second administrator there, the first can be removed. Without
// this the guard could be "never allow it", which is a different bug.
func TestAdministrator_CanBeRemovedWhenAnotherRemains(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "second-admin@test.local", DisplayName: "Second Admin",
		Role: user.RoleAdmin, Password: "a-real-passphrase",
	})
	require.NoError(t, err)

	admin := loggedInAdmin(t, h)
	res, body := admin.send(t, http.MethodPatch, "/api/v1/admin/users/"+h.adminID.String(),
		map[string]any{"role": "staff"})
	require.Equal(t, http.StatusOK, res.StatusCode,
		"an administrator with a colleague should still be demotable: %s", body)
}

// loggedInAdmin is a signed-in session for the seeded administrator.
//
// Acting on an administrator is refused to machine credentials, so the API
// key the rest of the suite uses cannot reach these routes at all.
func loggedInAdmin(t *testing.T, h *harness) *session {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	return s
}
