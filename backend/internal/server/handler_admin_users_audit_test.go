package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #306: clearing a factor or resetting a password used to leave no trace.
// These confirm the two web paths now write one audit entry each, naming the
// administrator who did it.

func TestAdminResetMFA_WritesAnAuditEntryNamingTheAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
		map[string]any{"reset_mfa": true})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "mfa_reset", entries[0].Action)
	require.NotNil(t, entries[0].ActorID)
	require.Equal(t, h.adminID, *entries[0].ActorID)
}

func TestAdminPasswordReset_WritesAnAuditEntryNamingTheAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users/"+h.staffID.String()+"/password",
		map[string]any{"new_password": "a-brand-new-password"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "password_reset_by_admin", entries[0].Action)
	require.NotNil(t, entries[0].ActorID)
	require.Equal(t, h.adminID, *entries[0].ActorID)
}

// A rejected reset attempt (bad target, wrong shape) must not still leave
// something in the trail claiming it happened.
func TestAdminResetMFA_ANonexistentTargetWritesNoAuditEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	bogus := uuid.New()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+bogus.String(),
		map[string]any{"reset_mfa": true})
	res.Body.Close()
	require.NotEqual(t, http.StatusOK, res.StatusCode)

	entries, err := h.auditStore.ListByEntity(ctx, "user", bogus, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}
