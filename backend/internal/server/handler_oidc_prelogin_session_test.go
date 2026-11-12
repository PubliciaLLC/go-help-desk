package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// sessionLifetimeLeft reads how long the row behind these cookies has left.
func sessionLifetimeLeft(t *testing.T, h *harness, cookies []*http.Cookie) time.Duration {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	sess, err := h.sessions.Get(req, auth.SessionName)
	require.NoError(t, err)
	require.False(t, sess.IsNew, "the cookie does not point at a stored session")

	var secs float64
	require.NoError(t, h.tx.QueryRowContext(context.Background(),
		`SELECT EXTRACT(EPOCH FROM expires_at - clock_timestamp()) FROM sessions WHERE id = $1`,
		sess.ID).Scan(&secs))
	return time.Duration(secs * float64(time.Second))
}

// GET /auth/oidc/login is anonymous, unthrottled and writes a row. A row that
// lives the full seven days makes every hit a week of storage; the session only
// has to outlive one trip to the identity provider.
func TestOIDCLogin_AnonymousSessionIsShortLived(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	_, _, cookies := oh.startOIDCLoginFull(t, nil)

	left := sessionLifetimeLeft(t, oh.harness, cookies)
	require.LessOrEqual(t, left, 15*time.Minute, "a pre-login session must not live the full session lifetime")
	require.Greater(t, left, 5*time.Minute, "long enough for a person to sign in at the identity provider")

	for _, c := range cookies {
		if c.Name == auth.SessionName {
			require.InDelta(t, 600, c.MaxAge, 30, "the cookie dies with the row")
		}
	}
}

// Starting an OIDC login from a browser that is already signed in must not
// shorten that sign-in: the login keeps the existing session on purpose.
func TestOIDCLogin_SignedInSessionKeepsItsLifetime(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()

	s := &session{h: oh.harness}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	_, _, cookies := oh.startOIDCLoginFull(t, s.jar)

	require.Greater(t, sessionLifetimeLeft(t, oh.harness, cookies), 24*time.Hour)
}
