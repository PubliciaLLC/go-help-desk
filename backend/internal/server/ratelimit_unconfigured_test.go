package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
)

// A Server built without WithRateLimitStore has no in-memory fallback to drift
// to: every limiter that is on refuses (an error, never an allow). That makes a
// deployment that forgot the wiring fail loudly. With no store at all the first
// use would be a nil dereference, which is why New installs one that errors.
func TestServerWithoutARateLimitStoreRefuses(t *testing.T) {
	s := routeOnlyServer(nil)
	ctx := context.Background()

	for name, l := range map[string]*ratelimit.Limiter{
		"guest resend":   s.guestResendLimiter,
		"guest upload":   s.guestUploadLimiter,
		"guest refusal":  s.guestRefusalLimiter,
		"reporter_email": s.onBehalfMailLimiter,
		"reset mail":     s.passwordResetLimiter,
		"template test":  s.templateTestLimiter,
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := l.Allow(ctx, "k")
			require.Error(t, err)
			require.False(t, ok, "no store, no count, no allow")

			ok, err = l.AllowOnce(ctx, "k", "row")
			require.Error(t, err)
			require.False(t, ok)

			_, err = l.Exceeded(ctx, "k")
			require.Error(t, err)
		})
	}
}
