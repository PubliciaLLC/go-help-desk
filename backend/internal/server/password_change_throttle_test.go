package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// PATCH /me/password checks the current password, and nothing throttled it:
// anyone holding a session (a borrowed machine, a stolen cookie) could guess
// it as fast as bcrypt allows. Wrong guesses now spend a per-user budget, with
// the same limits and the same tarpit as sign-in (see handleLocalLogin).

func changePasswordWith(t *testing.T, s *session, current string) int {
	t.Helper()
	res, _ := s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": current, "new_password": "a-much-better-password"})
	return res.StatusCode
}

func TestChangePasswordThrottle_RefusesAfterTheBudgetIsSpent(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 3)
	defer cleanup()
	s := loggedIn(t, h)

	for i := 1; i <= 3; i++ {
		require.Equal(t, http.StatusBadRequest, changePasswordWith(t, s, "wrong"), "attempt %d", i)
	}
	res, _ := s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "wrong", "new_password": "a-much-better-password"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "the budget is spent")
	require.NotEmpty(t, res.Header.Get("Retry-After"))
}

func TestChangePasswordThrottle_TheRightPasswordStillWorks(t *testing.T) {
	t.Run("within the budget", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		s := loggedIn(t, h)

		require.Equal(t, http.StatusBadRequest, changePasswordWith(t, s, "wrong"))
		require.Equal(t, http.StatusBadRequest, changePasswordWith(t, s, "wrong"))
		require.Equal(t, http.StatusNoContent, changePasswordWith(t, s, "password"))
	})

	// Nothing here may lock somebody out of their own account: an over-budget
	// caller waits, and the correct password is still accepted.
	t.Run("after the budget is spent", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 2)
		defer cleanup()
		s := loggedIn(t, h)

		for i := 0; i < 3; i++ {
			changePasswordWith(t, s, "wrong")
		}
		require.Equal(t, http.StatusNoContent, changePasswordWith(t, s, "password"))
	})
}

func TestChangePasswordThrottle_UsersAreIndependent(t *testing.T) {
	h, cleanup := newHarnessWithRateLimit(t, 2)
	defer cleanup()
	staff := loggedIn(t, h)
	admin := loggedInAdmin(t, h)

	for i := 0; i < 3; i++ {
		changePasswordWith(t, staff, "wrong")
	}
	require.Equal(t, http.StatusTooManyRequests, changePasswordWith(t, staff, "wrong"), "precondition: staff is throttled")

	require.Equal(t, http.StatusBadRequest, changePasswordWith(t, admin, "wrong"),
		"one user's wrong guesses must not throttle another")
}

// The over-budget wait. Without it the 429 alone limits nothing: the password
// is checked before the counter is consulted, so a guesser who ignores the
// status code is bounded only by bcrypt. Same shape as the sign-in tarpit tests
// in login_tarpit_test.go; the default harness uses a 1ms delay, which would
// not notice the wait being deleted.
func TestChangePasswordTarpit_SlowsAnOverBudgetUser(t *testing.T) {
	const (
		limit = 2
		delay = 150 * time.Millisecond
	)
	h, cleanup := newHarnessWithThrottle(t, limit, delay)
	defer cleanup()
	s := loggedIn(t, h)

	started := time.Now()
	for range limit {
		changePasswordWith(t, s, "wrong")
	}
	require.Less(t, time.Since(started), delay, "guesses inside the budget should not be held up")

	started = time.Now()
	changePasswordWith(t, s, "wrong")
	require.GreaterOrEqual(t, time.Since(started), delay, "an over-budget guess was answered immediately")

	// Slowed, never locked out.
	require.Equal(t, http.StatusNoContent, changePasswordWith(t, s, "password"),
		"the correct password must be honoured even with the budget spent")
}
