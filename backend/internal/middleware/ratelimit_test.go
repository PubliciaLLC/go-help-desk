package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

func TestRateLimiter(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	send := func(h http.Handler, addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/auth/local/login", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	t.Run("allows up to the limit then refuses", func(t *testing.T) {
		h := authmw.NewRateLimiter(3, time.Minute).Middleware(ok)
		for i := 1; i <= 3; i++ {
			require.Equal(t, http.StatusOK, send(h, "10.0.0.1:1111"), "attempt %d", i)
		}
		require.Equal(t, http.StatusTooManyRequests, send(h, "10.0.0.1:1111"),
			"the attempt past the limit must be refused")
	})

	t.Run("counts per address, not globally", func(t *testing.T) {
		h := authmw.NewRateLimiter(1, time.Minute).Middleware(ok)
		require.Equal(t, http.StatusOK, send(h, "10.0.0.1:1111"))
		require.Equal(t, http.StatusTooManyRequests, send(h, "10.0.0.1:1111"))
		// A different client must be unaffected by the first one's spending.
		require.Equal(t, http.StatusOK, send(h, "10.0.0.2:2222"))
	})

	// The port changes on every connection; keying on the full RemoteAddr
	// would hand an attacker a fresh budget per request.
	t.Run("ignores the source port", func(t *testing.T) {
		h := authmw.NewRateLimiter(1, time.Minute).Middleware(ok)
		require.Equal(t, http.StatusOK, send(h, "10.0.0.3:1111"))
		require.Equal(t, http.StatusTooManyRequests, send(h, "10.0.0.3:2222"))
	})

	// A forged header must not mint a new identity.
	t.Run("ignores X-Forwarded-For", func(t *testing.T) {
		h := authmw.NewRateLimiter(1, time.Minute).Middleware(ok)
		req := func(xff string) int {
			r := httptest.NewRequest(http.MethodPost, "/auth/local/login", nil)
			r.RemoteAddr = "10.0.0.4:1111"
			r.Header.Set("X-Forwarded-For", xff)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			return rr.Code
		}
		require.Equal(t, http.StatusOK, req("1.2.3.4"))
		require.Equal(t, http.StatusTooManyRequests, req("5.6.7.8"))
	})

	t.Run("the window resets", func(t *testing.T) {
		h := authmw.NewRateLimiter(1, 30*time.Millisecond).Middleware(ok)
		require.Equal(t, http.StatusOK, send(h, "10.0.0.5:1111"))
		require.Equal(t, http.StatusTooManyRequests, send(h, "10.0.0.5:1111"))

		require.Eventually(t, func() bool {
			return send(h, "10.0.0.5:1111") == http.StatusOK
		}, time.Second, 10*time.Millisecond, "the window must expire and allow again")
	})

	// 0 is how the test harness switches it off.
	t.Run("a limit of zero disables it", func(t *testing.T) {
		h := authmw.NewRateLimiter(0, time.Minute).Middleware(ok)
		for i := 0; i < 50; i++ {
			require.Equal(t, http.StatusOK, send(h, "10.0.0.6:1111"))
		}
	})

	t.Run("refusal carries Retry-After and a JSON envelope", func(t *testing.T) {
		h := authmw.NewRateLimiter(1, time.Minute).Middleware(ok)
		send(h, "10.0.0.7:1111")

		req := httptest.NewRequest(http.MethodPost, "/auth/local/login", nil)
		req.RemoteAddr = "10.0.0.7:1111"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusTooManyRequests, rr.Code)
		require.NotEmpty(t, rr.Header().Get("Retry-After"))
		require.Contains(t, rr.Body.String(), "rate_limited")
	})
}
