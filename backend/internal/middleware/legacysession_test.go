package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

func TestExpireLegacySession(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("clears the legacy cookie when present", func(t *testing.T) {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: auth.LegacySessionName, Value: "stale-payload"})
		rec := httptest.NewRecorder()

		authmw.ExpireLegacySession(true, next).ServeHTTP(rec, req)

		require.True(t, reached, "the request must still be served")

		var cleared *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.LegacySessionName {
				cleared = c
			}
		}
		require.NotNil(t, cleared, "the legacy cookie must be expired")
		require.Equal(t, "", cleared.Value)
		require.Less(t, cleared.MaxAge, 0, "a negative MaxAge is what deletes it")
		require.True(t, cleared.Secure, "the deletion must carry Secure on an HTTPS instance")
		require.False(t, cleared.Expires.IsZero(), "the deletion must carry an expiry too")
		require.True(t, cleared.Expires.Before(time.Now()), "and it must be in the past")
		require.True(t, cleared.HttpOnly)
		require.Equal(t, "/", cleared.Path,
			"a cookie set at / is only deleted by a Set-Cookie at /")
	})

	// A browser on plain HTTP discards a Set-Cookie carrying Secure, so an
	// unconditional Secure would stop the deletion reaching the deployments most
	// likely to still hold the old cookie.
	t.Run("omits Secure when the instance is not served over HTTPS", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: auth.LegacySessionName, Value: "stale-payload"})
		rec := httptest.NewRecorder()

		authmw.ExpireLegacySession(false, next).ServeHTTP(rec, req)

		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		require.False(t, cookies[0].Secure)
	})

	t.Run("does not touch the current cookie", func(t *testing.T) {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionName, Value: "live-session"})
		rec := httptest.NewRecorder()

		authmw.ExpireLegacySession(true, next).ServeHTTP(rec, req)

		require.True(t, reached)
		for _, c := range rec.Result().Cookies() {
			require.NotEqual(t, auth.SessionName, c.Name,
				"expiring the legacy cookie must never disturb a live session")
		}
	})

	t.Run("sets nothing when no legacy cookie is presented", func(t *testing.T) {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		authmw.ExpireLegacySession(true, next).ServeHTTP(rec, req)

		require.True(t, reached)
		require.Empty(t, rec.Result().Cookies(),
			"a browser that never held the old cookie must not be sent one")
	})
}
