package server_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// Retry-After on a 429 from the shared sixty-second window is the time left in
// that window, not sixty seconds every time (#421). A client that obeyed the
// old header waited up to a minute longer than it had to.
func TestRetryAfter_IsTheTimeLeftInTheWindow(t *testing.T) {
	type route struct {
		name   string
		method string
		path   string
		body   func(i int) any
	}
	routes := []route{
		{"login", http.MethodPost, "/api/v1/auth/local/login",
			func(int) any { return map[string]any{"email": "staff@test.local", "password": "wrong-password"} }},
		{"signup", http.MethodPost, "/api/v1/auth/signup",
			func(i int) any { return map[string]any{"email": "x" + strconv.Itoa(i) + "@test.local"} }},
		{"guest submit", http.MethodPost, "/api/v1/guest/tickets",
			func(int) any { return map[string]any{} }},
		{"guest resend", http.MethodPost, "/api/v1/guest/resend",
			func(int) any { return map[string]any{} }},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			h, cleanup := newHarnessWithRateLimit(t, 2)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))

			var refused *http.Response
			for i := 0; i < 6 && refused == nil; i++ {
				res := h.doUnauth(t, rt.method, rt.path, rt.body(i))
				res.Body.Close()
				if res.StatusCode == http.StatusTooManyRequests {
					refused = res
				}
			}
			require.NotNil(t, refused, "the budget was never spent")
			first, err := strconv.Atoi(refused.Header.Get("Retry-After"))
			require.NoError(t, err)
			require.GreaterOrEqual(t, first, 1)
			require.LessOrEqual(t, first, 60, "never more than the window")

			time.Sleep(1100 * time.Millisecond)
			res := h.doUnauth(t, rt.method, rt.path, rt.body(99))
			res.Body.Close()
			require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
			second, err := strconv.Atoi(res.Header.Get("Retry-After"))
			require.NoError(t, err)
			require.Less(t, second, first, "the wait must shrink as the window runs down")
		})
	}
}
