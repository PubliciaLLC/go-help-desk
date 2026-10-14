package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Retry-After is whole seconds, rounded up so a client that waits exactly that
// long is not refused again, and never 0, which a client reads as "now".
func TestTooManyAttempts_RetryAfterRoundsUpAndIsAtLeastOne(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{time.Minute, "60"},
		{59*time.Second + time.Millisecond, "60"},
		{1500 * time.Millisecond, "2"},
		{time.Millisecond, "1"},
		{0, "1"},
		{-time.Second, "1"},
		{15 * time.Minute, "900"},
	} {
		w := httptest.NewRecorder()
		tooManyAttempts(w, tc.in)
		require.Equal(t, 429, w.Code)
		require.Equal(t, tc.want, w.Header().Get("Retry-After"), "for %v", tc.in)
	}
}
