package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
