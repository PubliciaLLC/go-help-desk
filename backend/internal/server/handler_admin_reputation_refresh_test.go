package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// A refresh interval nobody recognises is refused at the write, not silently
// swapped for the default on the read.
//
// Same rule as the scan policy and the provider beside it, and here the
// ignored value decides how often customers' file hashes leave this server. An
// operator who typed "fortnightly" and saw a 204 believes their instance
// re-checks every two weeks; an operator who typed "Never" to stop spending
// their allowance believes it has stopped.
func TestSettings_RefusesAReputationRefreshNobodyRecognises(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := adminSession(t, h)

	t.Run("refused, and the message names the choices", func(t *testing.T) {
		for _, bad := range []string{"fortnightly", "Weekly", "", "7d", "off", "daily", " never"} {
			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAttachmentReputationRefresh: bad})
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"refresh %q was accepted: %s", bad, body)
			require.Contains(t, string(body), "invalid_reputation_refresh")
			require.Contains(t, string(body), "biweekly")
			require.Contains(t, string(body), "never")
		}
	})

	// A non-string is a bad request rather than a 500 or a silently stored
	// number.
	t.Run("a value that is not a string is refused too", func(t *testing.T) {
		res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAttachmentReputationRefresh: 14})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", body)
	})

	// The companion that stops the fix being "refuse everything", and that
	// checks the write actually lands where the reader looks.
	t.Run("every real interval is accepted and takes effect", func(t *testing.T) {
		cases := []struct {
			value string
			want  time.Duration
		}{
			{"weekly", 7 * 24 * time.Hour},
			{"monthly", 30 * 24 * time.Hour},
			{"quarterly", 90 * 24 * time.Hour},
			{"never", 0},
			{"biweekly", 14 * 24 * time.Hour},
		}
		for _, tc := range cases {
			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAttachmentReputationRefresh: tc.value})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "refresh %q: %s", tc.value, body)
			require.Equal(t, tc.value, h.adminSvc.ReputationRefresh(context.Background()))
			require.Equal(t, tc.want, h.adminSvc.ReputationRefreshInterval(context.Background()))
		}
	})

	// A bad value takes the rest of the write down with it, or an operator
	// changing two things gets one of them.
	t.Run("a bad interval rejects the whole write", func(t *testing.T) {
		before := h.adminSvc.ReputationRefresh(context.Background())
		res, _ := s.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
			"site_name":                          "Changed By A Rejected Write",
			admin.KeyAttachmentReputationRefresh: "whenever",
		})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, before, h.adminSvc.ReputationRefresh(context.Background()))

		name, _ := h.adminSvc.GetString(context.Background(), admin.KeySiteName)
		require.NotEqual(t, "Changed By A Rejected Write", name,
			"the valid half of a refused write must not land")
	})
}

// The interval is session-gated, like the provider and the key it governs.
//
// A leaked API key that can stretch this to "never" freezes every verdict on
// the instance at whatever it said the day the key was stolen, and nothing on
// any page would say so.
func TestSettings_ReputationRefreshIsSessionGated(t *testing.T) {
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyAttachmentReputationRefresh)
}
