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

// A caller that disconnects right after the mutation commits must not get
// to decide whether it left a trace — that's the exact actor #306 is
// worried about, and it's why user.Service detaches with
// context.WithoutCancel before touching the store. Calling the service
// directly with an already-canceled context is the deterministic way to
// prove it: pgx checks ctx.Err() up front and refuses immediately if the
// context isn't detached, so this fails hard (not just "usually passes")
// without the fix, rather than needing a timing sweep against a real
// disconnect.
func TestResetMFA_SucceedsAndIsAuditedEvenIfTheCallerHasAlreadyDisconnected(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	actorID := h.adminID
	err := h.userSvc.ResetMFA(canceled, h.staffID, &actorID)
	require.NoError(t, err, "a caller that has already disconnected must not turn a completed reset into a reported failure")

	got, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	require.False(t, got.MFAEnabled, "the reset itself must still have applied")

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the caller disconnecting must not suppress the audit entry")
	require.Equal(t, "mfa_reset", entries[0].Action)
}

// Same contract as the MFA reset above, for the other write path.
func TestAdminSetPassword_SucceedsAndIsAuditedEvenIfTheCallerHasAlreadyDisconnected(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	actorID := h.adminID
	err := h.userSvc.AdminSetPassword(canceled, h.staffID, "a-brand-new-password", &actorID)
	require.NoError(t, err, "a caller that has already disconnected must not turn a completed reset into a reported failure")

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the caller disconnecting must not suppress the audit entry")
	require.Equal(t, "password_reset_by_admin", entries[0].Action)
}

// A rejected reset attempt (bad target, wrong shape) must not still leave
// something in the trail claiming it happened.
//
// Deliberately a logged-in SESSION, not h.doAsAdmin's API key: an API key
// hits denyMachineTargetingAdmin's own GetByID 404 before ResetMFA is ever
// called, which made an earlier version of this test pass without
// exercising ResetMFA's nonexistent-target behavior at all. A real session
// reaches the handler's ResetMFA call directly. Found by adversarial review
// of #306: ClearMFA/AdminSetPassword were sqlc :exec queries, so an UPDATE
// matching zero rows reported no error, and the audit entry was written
// unconditionally — a permanent, falsely-attributed row for an account that
// never existed.
func TestAdminResetMFA_ANonexistentTargetWritesNoAuditEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	bogus := uuid.New()
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/admin/users/"+bogus.String(),
		map[string]any{"reset_mfa": true})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", body)

	entries, err := h.auditStore.ListByEntity(ctx, "user", bogus, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries, "a 404'd reset must not still be recorded as having happened")
}

// The same defect, on the password-reset path — worse there, because the
// handler had no re-fetch afterward to catch it: a nonexistent target got a
// full 204 success.
func TestAdminPasswordReset_ANonexistentTargetFailsAndWritesNoAuditEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	bogus := uuid.New()
	s := loggedInAdmin(t, h)

	res, body := s.send(t, http.MethodPost, "/api/v1/admin/users/"+bogus.String()+"/password",
		map[string]any{"new_password": "a-brand-new-password"})
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"a reset against an account that does not exist must not answer success: body: %s", body)

	entries, err := h.auditStore.ListByEntity(ctx, "user", bogus, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries, "a failed reset must not still be recorded as having happened")
}
