package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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

		authmw.ExpireLegacySession(next).ServeHTTP(rec, req)

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
	})

	t.Run("does not touch the current cookie", func(t *testing.T) {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionName, Value: "live-session"})
		rec := httptest.NewRecorder()

		authmw.ExpireLegacySession(next).ServeHTTP(rec, req)

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

		authmw.ExpireLegacySession(next).ServeHTTP(rec, req)

		require.True(t, reached)
		require.Empty(t, rec.Result().Cookies(),
			"a browser that never held the old cookie must not be sent one")
	})
}
