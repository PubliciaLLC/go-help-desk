package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/stretchr/testify/require"
)

// TestUpdateSettings_RejectsNullGuestSubmissionEnabled pins #177's second
// half: guest_submission_enabled had no validation case in
// handleUpdateSettings at all, so a JSON null was accepted and then read
// back as false by GetBool's own silent fallback — the "accepted and then
// ignored" shape this handler refuses everywhere else. Same failure mode
// #304 found and fixed for the SSO keys: encoding/json's Unmarshal treats
// null into a non-pointer destination as a silent no-op, not an error, so
// a bare type check alone would still miss it.
func TestUpdateSettings_RejectsNullGuestSubmissionEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	sess := signInAsAdmin(t, h)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"guest_submission_enabled": nil})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
	require.True(t, h.adminSvc.GuestSubmissionEnabled(ctx),
		"the null write must not have been persisted")
}

// TestUpdateSettings_RejectsNonBooleanGuestSubmissionEnabled is the type-
// mismatch half of the same finding: a JSON string is not silently coerced
// or accepted.
func TestUpdateSettings_RejectsNonBooleanGuestSubmissionEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	sess := signInAsAdmin(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"guest_submission_enabled": "true"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
	require.False(t, h.adminSvc.GuestSubmissionEnabled(ctx),
		"the malformed write must not have been persisted")

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"guest_submission_enabled": nil})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
	require.False(t, h.adminSvc.GuestSubmissionEnabled(ctx),
		"the null write must not have been persisted")
}

// TestUpdateSettings_GuestSubmissionEnabled_AdminCanSet confirms the fix
// only closes the machine-credential and malformed-value doors — a signed-in
// administrator sending a genuine boolean still works exactly as before.
func TestUpdateSettings_GuestSubmissionEnabled_AdminCanSet(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	sess := signInAsAdmin(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"guest_submission_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.True(t, h.adminSvc.GuestSubmissionEnabled(ctx))
}
