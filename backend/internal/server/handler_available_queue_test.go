package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// available_queue_roles is a JSON array of role names, checked at save (#676).
// Only admin and staff may be listed: a requester has no queues. A value that
// is not an array of strings, or null, is refused as every typed setting is.
func TestAvailableQueueRoles_Accepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	for _, roles := range [][]string{{}, {"admin"}, {"admin", "staff"}, {"staff", "staff"}} {
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAvailableQueueRoles: roles})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%v: %s", roles, body)
	}
}

func TestAvailableQueueRoles_RefusedValues(t *testing.T) {
	cases := []struct {
		name  string
		value any
		code  string
	}{
		{"user role", []string{"user"}, "bad_request"},
		{"root role", []string{"root"}, "bad_request"},
		{"staff as a string", "staff", "bad_request"},
		{"null", nil, "bad_request"},
		{"array with a number", []any{1}, "bad_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)

			before, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)

			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAvailableQueueRoles: c.value})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

			after, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)
			require.Equal(t, string(before[admin.KeyAvailableQueueRoles]), string(after[admin.KeyAvailableQueueRoles]),
				"the stored value must be left as it was")
		})
	}
}

// The message names the two roles that may be listed, so an administrator who
// typed "user" learns what is accepted.
func TestAvailableQueueRoles_RefusalNamesTheAcceptedRoles(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAvailableQueueRoles: []string{"user"}})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "available_queue_roles may list only admin and staff")
}

// Session-only: a machine credential is refused before the value is read.
func TestAvailableQueueRoles_RefusedToMachineCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAvailableQueueRoles: []string{"staff"}})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
	require.Equal(t, "session_required", errCode(t, b))
	require.False(t, h.adminSvc.AvailableQueueAllowed(context.Background(), "staff"))
}
