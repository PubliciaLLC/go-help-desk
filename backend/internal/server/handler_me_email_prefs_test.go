package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The three staff-email switches on a person's own profile (#441).

type emailPrefs struct {
	Assigned bool `json:"email_notify_assigned"`
	Activity bool `json:"email_notify_activity"`
	Group    bool `json:"email_notify_group"`
}

func TestMe_ReportsTheEmailSwitchesOnByDefault(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var got emailPrefs
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, emailPrefs{true, true, true}, got)
}

func TestMe_PatchChangesOnlyTheSwitchesGiven(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_group": false})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var got emailPrefs
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, emailPrefs{true, true, false}, got, "the response is the profile as saved")

	res, body = s.send(t, http.MethodPatch, "/api/v1/me/email-preferences",
		map[string]any{"email_notify_assigned": false, "email_notify_group": true})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, body = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, emailPrefs{false, true, true}, got, "it persisted")
}

func TestMe_PatchRefusesWhatIsNotABoolean(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedIn(t, h)

	for name, body := range map[string]any{
		"a string": map[string]any{"email_notify_assigned": "no"},
		"a number": map[string]any{"email_notify_group": 0},
	} {
		t.Run(name, func(t *testing.T) {
			res, b := s.send(t, http.MethodPatch, "/api/v1/me/email-preferences", body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
		})
	}
	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.True(t, u.EmailNotifyAssigned && u.EmailNotifyGroup, "a refusal changed something")
}

// It is a personal setting of a signed-in person, not something a key or a
// requester account has.
func TestMe_PatchIsForSignedInStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("an API key", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_assigned": false})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		u, err := h.userSvc.GetByID(context.Background(), h.staffID)
		require.NoError(t, err)
		require.True(t, u.EmailNotifyAssigned)
	})

	t.Run("a requester account", func(t *testing.T) {
		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "user@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		res, _ = s.send(t, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_assigned": false})
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("nobody signed in", func(t *testing.T) {
		res := h.doUnauth(t, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_assigned": false})
		res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}

// What actually applies to this person, which is not always what they chose:
// an administrator can force a switch on or off for everyone (#441).

type switchState struct {
	Enabled bool `json:"enabled"`
	Forced  bool `json:"forced"`
}

type emailState struct {
	EmailNotifications *struct {
		Assigned switchState `json:"assigned"`
		Activity switchState `json:"activity"`
		Group    switchState `json:"group"`
	} `json:"email_notifications"`
}

func meState(t *testing.T, s *session, method, path string, body any) emailState {
	t.Helper()
	res, b := s.send(t, method, path, body)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var got emailState
	require.NoError(t, json.Unmarshal(b, &got))
	require.NotNil(t, got.EmailNotifications, "body: %s", b)
	return got
}

func TestMe_ReportsWhichSwitchesAreForcedAndWhatAppliesToThem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	s := loggedIn(t, h)

	// Nothing overridden: what they chose is what applies.
	got := meState(t, s, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, switchState{Enabled: true}, got.EmailNotifications.Assigned)
	require.Equal(t, switchState{Enabled: true}, got.EmailNotifications.Activity)
	require.Equal(t, switchState{Enabled: true}, got.EmailNotifications.Group)

	// They turn two off; the administrator forces one of them on and the third off.
	off := false
	require.NoError(t, h.userSvc.SetEmailPreferences(ctx, h.staffID, user.EmailPreferencesPatch{Assigned: &off, Activity: &off}))
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailAssignedOverride, "on"))
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyStaffEmailGroupOverride, "off"))

	got = meState(t, s, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, switchState{Enabled: true, Forced: true}, got.EmailNotifications.Assigned, "forced on over their off")
	require.Equal(t, switchState{Enabled: false}, got.EmailNotifications.Activity, "theirs, off, not forced")
	require.Equal(t, switchState{Enabled: false, Forced: true}, got.EmailNotifications.Group, "forced off over their on")

	// The saved choice itself is still reported as they made it.
	var raw emailPrefs
	_, b := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.NoError(t, json.Unmarshal(b, &raw))
	require.Equal(t, emailPrefs{false, false, true}, raw)

	// And the same state comes back from saving.
	got = meState(t, s, http.MethodPatch, "/api/v1/me/email-preferences", map[string]any{"email_notify_activity": true})
	require.Equal(t, switchState{Enabled: true}, got.EmailNotifications.Activity)
	require.Equal(t, switchState{Enabled: true, Forced: true}, got.EmailNotifications.Assigned)
}

func TestMe_AnOverrideOfUserIsNotForced(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyStaffEmailAssignedOverride, "user"))
	got := meState(t, loggedIn(t, h), http.MethodGet, "/api/v1/me", nil)
	require.False(t, got.EmailNotifications.Assigned.Forced)
}

// A requester is never sent staff mail, so has no switches to report.
func TestMe_ARequesterHasNoEmailNotifications(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	res, b := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotContains(t, string(b), "email_notifications")
}
