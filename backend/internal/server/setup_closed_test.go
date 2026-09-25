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

// Setup stays closed after the last account is disabled or deleted.
//
// The gate counted live accounts — not deleted, not disabled — so soft-delete
// or disable every account and the route reopened: GET /setup/status answered
// {"needed": true} to anyone on the internet, and POST /setup handed them an
// administrator over the existing data. Every ticket, every customer, every
// attachment.
//
// Not a hypothetical path. An administrator can disable or delete their own,
// sole, admin account; nothing in the user handler stops them. One mistake on
// a public instance and the front door is open.
//
// "Setup is permanently blocked once complete" is what the design says, and
// what this pins. Nothing hard-deletes a user, so counting rows says it.
func TestSetup_StaysClosedWhenEveryAccountIsGone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// It is closed to begin with, because the harness seeded accounts.
	require.False(t, setupNeeded(t, h), "the harness has users, so setup should be closed")

	// Take away everything the system will let us take away. Both routes
	// leave a row behind, which is every route there is: nothing here hard-
	// deletes a user.
	for _, id := range []uuid.UUID{h.staffID, h.userID} {
		require.NoError(t, h.userSvc.Disable(ctx, id))
		require.NoError(t, h.userSvc.SoftDelete(ctx, id))
	}

	// And the last administrator cannot be taken away at all — which is the
	// stronger half of this guarantee, and the reason the state this test was
	// originally written for is no longer reachable through the API.
	require.ErrorIs(t, h.userSvc.Disable(ctx, h.adminID), user.ErrLastAdmin)
	require.ErrorIs(t, h.userSvc.SoftDelete(ctx, h.adminID), user.ErrLastAdmin)

	require.False(t, setupNeeded(t, h),
		"the setup route reopened, so anyone can now create an administrator on this instance")

	res := h.do(t, http.MethodPost, "/api/v1/setup", map[string]any{
		"email": "attacker@example.com", "display_name": "Attacker", "password": "hunter22hunter22",
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode,
		"an anonymous request minted an administrator over the existing data")
}

func setupNeeded(t *testing.T, h *harness) bool {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/setup/status", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var payload struct {
		Needed bool `json:"needed"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&payload))
	return payload.Needed
}
