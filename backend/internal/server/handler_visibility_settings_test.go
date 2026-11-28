package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Who sees and who is given tickets is decided by these settings, so a key may
// not change them (CTO, 2026-10-16): a key that could switch ticket scope
// enforcement off, or point auto-assign at an account it controls, would widen
// what that account can see or be handed.
func TestVisibilitySettings_AreSessionOnly(t *testing.T) {
	cases := []struct {
		key   string
		value any
	}{
		{admin.KeyTicketScopeEnforced, true},
		{admin.KeyAutoAssignGroupID, uuid.NewString()},
		{admin.KeyAutoAssignUserIDs, []string{uuid.NewString()}},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			require.Contains(t, admin.AuthCriticalKeys(), tc.key)

			res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{tc.key: tc.value})
			b := readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
			require.Equal(t, "session_required", errCode(t, b))
			all, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)
			require.NotContains(t, all, tc.key, "nothing was stored")
		})
	}
}

func TestVisibilitySettings_ASessionCanStillChangeThem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	g, err := h.groupSvc.Create(context.Background(), "Desk", "")
	require.NoError(t, err)
	sess := signInAsAdmin(t, h)
	for key, value := range map[string]any{
		admin.KeyTicketScopeEnforced: true,
		admin.KeyAutoAssignGroupID:   g.ID.String(),
		admin.KeyAutoAssignUserIDs:   []string{h.staffID.String()},
	} {
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: value})
		require.Contains(t, []int{http.StatusNoContent, http.StatusOK}, res.StatusCode, "%s: %s", key, body)
	}
}
