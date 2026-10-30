package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// An account that has spent its budget is slowed down, one request at a time.
//
// The counter alone did not limit guessing. Password verification runs before
// the counter is consulted — deliberately, so a correct password is always
// honoured and nobody can be locked out of their own account by somebody
// else's wrong guesses — so the counter only changed the status code. With a
// limit of three, eight wrong guesses answered 401 401 401 429 429 429 429
// 429, and every one of those 429s had still run the password check. An
// attacker ignoring the status code had unlimited online guesses, bounded
// only by bcrypt. MFA is off by default, so on most instances that was the
// whole of the online defence.
//
// Two things are asserted, and the second is the one that matters. A delay
// that requests can take in parallel is not a limit on anything: fifty at
// once would each wait a second and still make fifty guesses in a second. So
// the waits have to be sequential per account.
func TestLoginTarpit_SlowsAnOverBudgetAccount(t *testing.T) {
	const (
		limit = 2
		delay = 150 * time.Millisecond
	)
	h, cleanup := newHarnessWithThrottle(t, limit, delay)
	defer cleanup()

	wrong := func() *http.Response {
		return h.do(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "not-the-password"})
	}

	// Spend the budget. These are not delayed.
	started := time.Now()
	for range limit {
		wrong().Body.Close()
	}
	require.Less(t, time.Since(started), delay,
		"requests inside the budget should not be held up")

	// The next one is.
	started = time.Now()
	res := wrong()
	res.Body.Close()
	require.GreaterOrEqual(t, time.Since(started), delay,
		"an over-budget guess was answered immediately")

	// And the right password still works. This is the property the whole
	// ordering exists to protect: nobody can be locked out of their own
	// account by someone else's guessing, only slowed down.
	ok := h.do(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode,
		"the correct password must be honoured even with the budget spent")
}

// The over-budget wait applies to a wrong guess, and the right password still
// gets in.
//
// The turn-taking and the bound on waiting are properties of the limiter and
// are tested there, in internal/middleware — this harness runs every test
// inside one database transaction, so a concurrent flood through it exercises
// the transaction rather than the tarpit.
func TestLoginTarpit_TheRightPasswordStillGetsIn(t *testing.T) {
	const (
		limit = 2
		delay = 150 * time.Millisecond
	)
	h, cleanup := newHarnessWithThrottle(t, limit, delay)
	defer cleanup()

	wrong := func() *http.Response {
		return h.do(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "not-the-password"})
	}
	for range limit {
		wrong().Body.Close()
	}

	// Over budget now: this one waits.
	began := time.Now()
	res := wrong()
	res.Body.Close()
	require.GreaterOrEqual(t, time.Since(began), delay,
		"an over-budget guess was answered immediately")

	// And the owner gets in, delayed but never refused.
	began = time.Now()
	ok := h.do(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode,
		"the correct password must be honoured with the budget spent")
	require.Less(t, time.Since(began), 4*delay,
		"the owner's own login was held open longer than the tarpit should ever hold anything")
}
