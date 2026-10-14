package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Signup and guest submission both end in an email (the verification link, the
// guest's ticket link). With email off they appear to work and deliver
// nothing, so turning either on is refused (#456). Already-on installs are
// not switched off and not refused on unrelated saves: the settings page
// PATCHes the whole object, so only a change from off to on counts.

func emailOn(c *config.Config) {
	c.SMTPHost = "smtp.example.test"
	c.SMTPFrom = "help@example.test"
}

type apiError struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func patchSettings(t *testing.T, h *harness, body map[string]any) (int, apiError) {
	t.Helper()
	sess := signInAsAdmin(t, h)
	res, raw := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", body)
	var e apiError
	_ = json.Unmarshal([]byte(raw), &e)
	return res.StatusCode, e
}

func TestUpdateSettings_EmailOff_RefusesTurningSignupOrGuestOn(t *testing.T) {
	for _, key := range []string{admin.KeySelfSignupEnabled, admin.KeyGuestSubmissionEnabled} {
		t.Run(key, func(t *testing.T) {
			h, cleanup := newHarness(t) // no SMTP: email is off
			defer cleanup()

			status, e := patchSettings(t, h, map[string]any{key: true})
			require.Equal(t, http.StatusBadRequest, status)
			require.Equal(t, "email_required", e.Error.Code)

			on, err := h.adminSvc.GetBool(context.Background(), key)
			if err == nil {
				require.False(t, on, "a refused write was stored")
			}
		})
	}
}

// A refused request writes nothing, including the other keys in the same body.
func TestUpdateSettings_EmailOff_RefusalWritesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	status, e := patchSettings(t, h, map[string]any{"site_name": "Renamed", admin.KeyGuestSubmissionEnabled: true})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "email_required", e.Error.Code)

	name, err := h.adminSvc.GetString(ctx, "site_name")
	if err == nil {
		require.NotEqual(t, "Renamed", name, "the rest of a refused request was written")
	}
}

// An install that already has it on, with email off, is only warned: every
// save of the settings page sends the whole object back, on included.
func TestUpdateSettings_EmailOff_AlreadyOnIsNotRefused(t *testing.T) {
	for _, key := range []string{admin.KeySelfSignupEnabled, admin.KeyGuestSubmissionEnabled} {
		t.Run(key, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, h.adminSvc.SetBool(ctx, key, true))

			status, _ := patchSettings(t, h, map[string]any{key: true, "site_name": "Renamed"})
			require.Equal(t, http.StatusNoContent, status, "an unrelated save was refused because the toggle was already on")

			// Turning it off is always allowed.
			status, _ = patchSettings(t, h, map[string]any{key: false})
			require.Equal(t, http.StatusNoContent, status)
			on, err := h.adminSvc.GetBool(ctx, key)
			require.NoError(t, err)
			require.False(t, on)
		})
	}
}

func TestUpdateSettings_EmailOff_OffToOffAndOmittedAreFine(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	status, _ := patchSettings(t, h, map[string]any{admin.KeySelfSignupEnabled: false, admin.KeyGuestSubmissionEnabled: false})
	require.Equal(t, http.StatusNoContent, status)
	status, _ = patchSettings(t, h, map[string]any{"site_name": "Renamed"})
	require.Equal(t, http.StatusNoContent, status)
}

func TestUpdateSettings_EmailOn_AllowsTurningSignupOrGuestOn(t *testing.T) {
	for _, key := range []string{admin.KeySelfSignupEnabled, admin.KeyGuestSubmissionEnabled} {
		t.Run(key, func(t *testing.T) {
			h, cleanup := newHarnessCfg(t, 0, "", emailOn)
			defer cleanup()

			status, _ := patchSettings(t, h, map[string]any{key: true})
			require.Equal(t, http.StatusNoContent, status)
			on, err := h.adminSvc.GetBool(context.Background(), key)
			require.NoError(t, err)
			require.True(t, on)
		})
	}
}

// Host without sender is not "email on": EmailEnabled needs both.
func TestUpdateSettings_HostWithoutSenderCountsAsEmailOff(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 0, "", func(c *config.Config) { c.SMTPHost = "smtp.example.test" })
	defer cleanup()
	status, e := patchSettings(t, h, map[string]any{admin.KeyGuestSubmissionEnabled: true})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "email_required", e.Error.Code)
}

// ── security-warnings: email ─────────────────────────────────────────────────

type emailWarnings struct {
	Email struct {
		Enabled                    bool     `json:"enabled"`
		Missing                    []string `json:"missing"`
		SignupWithoutEmail         bool     `json:"signup_without_email"`
		GuestSubmissionWithoutMail bool     `json:"guest_submission_without_email"`
		Failing                    bool     `json:"failing"`
		FailingCount               int      `json:"failing_count"`
		LastError                  string   `json:"last_error"`
	} `json:"email"`
}

func getEmailWarnings(t *testing.T, h *harness) emailWarnings {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var w emailWarnings
	decodeJSON(t, resp, &w)
	return w
}

func TestSecurityWarnings_EmailUnconfigured(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	w := getEmailWarnings(t, h)
	require.False(t, w.Email.Enabled)
	require.ElementsMatch(t, []string{"SMTP_HOST", "SMTP_FROM"}, w.Email.Missing)
	require.False(t, w.Email.SignupWithoutEmail)
	require.False(t, w.Email.GuestSubmissionWithoutMail)
	require.False(t, w.Email.Failing)
}

func TestSecurityWarnings_EmailNamesWhatIsMissing(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 0, "", func(c *config.Config) { c.SMTPHost = "smtp.example.test" })
	defer cleanup()
	w := getEmailWarnings(t, h)
	require.False(t, w.Email.Enabled)
	require.Equal(t, []string{"SMTP_FROM"}, w.Email.Missing)
}

// The existing-install case: signup or guest submission already on with email
// off is reported, because it was not switched off.
func TestSecurityWarnings_EmailOffWithSignupAndGuestOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

	w := getEmailWarnings(t, h)
	require.True(t, w.Email.SignupWithoutEmail)
	require.True(t, w.Email.GuestSubmissionWithoutMail)
}

func TestSecurityWarnings_EmailConfiguredAndHealthyReportsNothing(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 0, "", emailOn)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySelfSignupEnabled, true))

	w := getEmailWarnings(t, h)
	require.True(t, w.Email.Enabled)
	require.Empty(t, w.Email.Missing)
	require.False(t, w.Email.SignupWithoutEmail, "email is on; signup is fine")
	require.False(t, w.Email.Failing)
	require.Zero(t, w.Email.FailingCount)
	require.Empty(t, w.Email.LastError)
}

func TestSecurityWarnings_EmailFailingShowsTheLastError(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 0, "", emailOn)
	defer cleanup()
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, h.q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{
		ID: id, Channel: "email", Event: []byte(`{"type":"ticket.created"}`)}))
	require.NoError(t, h.q.FailNotification(ctx, dbgen.FailNotificationParams{
		ID: id, LastError: sql.NullString{String: "SMTP server does not offer AUTH but SMTP_USER is set", Valid: true}}))

	w := getEmailWarnings(t, h)
	require.True(t, w.Email.Failing)
	require.Equal(t, 1, w.Email.FailingCount)
	require.Equal(t, "SMTP server does not offer AUTH but SMTP_USER is set", w.Email.LastError)
}

// A webhook that fails is not an email problem.
func TestSecurityWarnings_EmailIgnoresOtherChannels(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 0, "", emailOn)
	defer cleanup()
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, h.q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{
		ID: id, Channel: "webhook", Event: []byte(`{"type":"ticket.created"}`)}))
	require.NoError(t, h.q.FailNotification(ctx, dbgen.FailNotificationParams{
		ID: id, LastError: sql.NullString{String: strings.Repeat("x", 10), Valid: true}}))

	require.False(t, getEmailWarnings(t, h).Email.Failing)
}
