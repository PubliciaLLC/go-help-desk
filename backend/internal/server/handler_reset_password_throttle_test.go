package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// POST /auth/reset-password is throttled per source address. A link is 256
// random bits, so the throttle is not what protects it; it bounds the bcrypt
// work an anonymous caller can make the server spend. Nothing exercised it.
func TestResetPasswordThrottle_IsApplied(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 2, "", emailOn)
	defer cleanup()

	attempt := func() int {
		res, _ := resetPassword(t, h, "not-a-real-token", "a-long-enough-password")
		return res.StatusCode
	}
	require.Equal(t, http.StatusUnprocessableEntity, attempt(), "first attempt reaches the token check")
	require.Equal(t, http.StatusUnprocessableEntity, attempt(), "second attempt reaches the token check")
	require.Equal(t, http.StatusTooManyRequests, attempt(), "the address's budget is spent")
}
