package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// #452: a half-signed-in session (password right, second factor owed) lives
// five minutes to enter a code and fifteen to enrol, then it is gone.
//
// The deadline is recorded when the session is created, so these tests sign in
// for real (proving the right window was chosen) and then move the stored
// deadline into the past, rather than waiting or injecting a clock.

// pendingSession signs staff in and returns the session. enrolled true means a
// code from an existing factor is owed; false means first-time enrolment is.
func pendingSession(t *testing.T, h *harness, enrolled bool) *session {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	if enrolled {
		enrollMFA(t, ctx, h.userSvc, h.staffID)
	}
	return staffPasswordLogin(t, h)
}

// storedSession loads the session behind s's cookie straight from the store.
func storedSession(t *testing.T, s *session) (*http.Request, auth.SessionData, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range s.jar {
		req.AddCookie(c)
	}
	sess, err := s.h.sessions.Get(req, auth.SessionName)
	require.NoError(t, err)
	if sess.IsNew {
		return req, auth.SessionData{}, false
	}
	sd, ok := sess.Values[auth.SessionDataKey].(auth.SessionData)
	require.True(t, ok)
	return req, sd, true
}

// rewrite replaces the stored payload of s's session.
func rewrite(t *testing.T, s *session, change func(*auth.SessionData)) {
	t.Helper()
	req, sd, ok := storedSession(t, s)
	require.True(t, ok, "session should exist")
	change(&sd)
	sess, err := s.h.sessions.Get(req, auth.SessionName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = sd
	require.NoError(t, s.h.sessions.Save(req, httptest.NewRecorder(), sess))
}

// age moves the deadline d into the past, as if the sign-in were d older.
func age(t *testing.T, s *session, d time.Duration) {
	t.Helper()
	rewrite(t, s, func(sd *auth.SessionData) { sd.PendingUntil = sd.PendingUntil.Add(-d) })
}

func sessionRowExists(t *testing.T, s *session) bool {
	t.Helper()
	_, _, ok := storedSession(t, s)
	return ok
}

func TestPendingMFA_LoginRecordsTheRightWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enrolled bool
		want     time.Duration
	}{
		{"code from an existing factor", true, 5 * time.Minute},
		{"first-time enrolment", false, 15 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := pendingSession(t, h, tc.enrolled)

			_, sd, ok := storedSession(t, s)
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(tc.want), sd.PendingUntil, 10*time.Second)

			// The cookie and row get the same lifetime, so an abandoned
			// sign-in clears itself instead of waiting out the seven days.
			require.NotEmpty(t, s.jar)
			require.InDelta(t, tc.want.Seconds(), s.jar[0].MaxAge, 10)
		})
	}
}

func TestPendingMFA_CodeEntry(t *testing.T) {
	verify := func(t *testing.T, s *session, code string) int {
		res, _ := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
		return res.StatusCode
	}
	goodCode := func(t *testing.T, h *harness) string {
		u, err := h.userSvc.GetByID(context.Background(), h.staffID)
		require.NoError(t, err)
		code, err := totp.GenerateCode(u.MFASecret, time.Now())
		require.NoError(t, err)
		return code
	}

	t.Run("works inside the window", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, true)
		age(t, s, 4*time.Minute)
		require.Equal(t, http.StatusNoContent, verify(t, s, goodCode(t, h)))
	})

	t.Run("refused after five minutes, and the row is deleted", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, true)
		age(t, s, 6*time.Minute)
		require.Equal(t, http.StatusUnauthorized, verify(t, s, goodCode(t, h)),
			"a correct code must not finish an expired sign-in")
		require.False(t, sessionRowExists(t, s), "the expired session row should be gone")
	})

	t.Run("a refused code does not use up an attempt", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, true)
		age(t, s, 6*time.Minute)
		require.Equal(t, http.StatusUnauthorized, verify(t, s, "000000"))

		// The budget is five wrong codes; the sixth is locked out. If the
		// refused request above had spent one, the fifth would already be 429.
		fresh := staffPasswordLogin(t, h)
		for i := 1; i <= 5; i++ {
			require.Equal(t, http.StatusUnauthorized, verify(t, fresh, "000001"), "guess %d", i)
		}
		require.Equal(t, http.StatusTooManyRequests, verify(t, fresh, "000001"))
	})

	t.Run("a pre-upgrade session with no deadline is refused", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, true)
		rewrite(t, s, func(sd *auth.SessionData) { sd.PendingUntil = time.Time{} })
		require.Equal(t, http.StatusUnauthorized, verify(t, s, goodCode(t, h)))
		require.False(t, sessionRowExists(t, s))
	})

	t.Run("signing in again works after the deadline", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, true)
		age(t, s, 6*time.Minute)
		require.Equal(t, http.StatusUnauthorized, verify(t, s, goodCode(t, h)))
		require.Equal(t, http.StatusNoContent, verify(t, staffPasswordLogin(t, h), goodCode(t, h)))
	})
}

func TestPendingMFA_PasskeyRoutesRefuseAnExpiredSession(t *testing.T) {
	for _, path := range []string{
		"/api/v1/auth/local/passkey/start",
		"/api/v1/auth/local/passkey/finish",
	} {
		t.Run(path, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := pendingSession(t, h, true)
			age(t, s, 6*time.Minute)
			res, body := s.send(t, http.MethodPost, path, map[string]any{})
			require.Equal(t, http.StatusUnauthorized, res.StatusCode, "body: %s", body)
			require.False(t, sessionRowExists(t, s))
		})
	}
}

func TestPendingMFA_Enrolment(t *testing.T) {
	t.Run("works at fourteen minutes, past the five-minute code window", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, false)
		age(t, s, 14*time.Minute)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		res, body = s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	})

	t.Run("refused after fifteen minutes, and the row is deleted", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, false)
		age(t, s, 16*time.Minute)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, "body: %s", body)
		require.False(t, sessionRowExists(t, s))
	})

	t.Run("passkey registration is refused after fifteen minutes too", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, false)
		age(t, s, 16*time.Minute)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, "body: %s", body)
	})

	t.Run("staging a secret does not extend the deadline", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, false)
		_, before, _ := storedSession(t, s)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		_, after, ok := storedSession(t, s)
		require.True(t, ok)
		require.True(t, before.PendingUntil.Equal(after.PendingUntil))
		require.NotEmpty(t, after.PendingMFASecret)
	})

	t.Run("a pre-upgrade session with no deadline is refused", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := pendingSession(t, h, false)
		rewrite(t, s, func(sd *auth.SessionData) { sd.PendingUntil = time.Time{} })
		res, _ := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}
