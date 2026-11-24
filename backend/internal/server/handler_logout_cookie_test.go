package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// Logging out has to delete the cookie, not just the row.
//
// handleLogout replaces session.Options wholesale with
// &sessions.Options{MaxAge: -1}, which drops Path along with everything else.
// The store used to write the deletion cookie from those options, so it went
// out with an empty Path and the browser scoped it to the request's own
// directory — /api/v1/auth/local. A cookie set at "/" is not deleted by a
// Set-Cookie at a different path, so the cookie stayed in the browser for the
// rest of its seven days. The session row was gone, so it granted nothing, but
// anything treating the cookie's presence as "probably signed in" was wrong,
// and the stale value sat on disk.
//
// The store now builds every session cookie from its configured options and
// takes only MaxAge from the session.
func TestLogout_DeletesTheCookieAtTheSamePathItWasSetOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	login := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	defer login.Body.Close()
	require.Equal(t, http.StatusOK, login.StatusCode)

	var set *http.Cookie
	for _, c := range login.Cookies() {
		if c.Name == auth.SessionName {
			set = c
		}
	}
	require.NotNil(t, set, "login must set the session cookie")
	require.Equal(t, "/", set.Path)
	require.True(t, set.HttpOnly)

	res := h.doUnauthWithCookie(t, http.MethodPost, "/api/v1/auth/local/logout", set)
	defer res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	var cleared *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionName {
			cleared = c
		}
	}
	require.NotNil(t, cleared, "logout must send a deletion cookie")
	require.Equal(t, "", cleared.Value)
	require.Less(t, cleared.MaxAge, 0)
	require.Equal(t, set.Path, cleared.Path,
		"a deletion at a different path does not delete anything")
	require.Equal(t, set.HttpOnly, cleared.HttpOnly)
	require.Equal(t, set.Secure, cleared.Secure)
	require.Equal(t, set.SameSite, cleared.SameSite)
}
