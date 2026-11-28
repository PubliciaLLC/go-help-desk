package server_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
)

// #621: every rate limit counts in the database, so a restart does not clear it
// and a second replica shares it. A limiter that quietly went back to counting
// in memory would still pass every behavioural test in this package, which all
// run in one process, so these look for the row.
//
// The harness runs in one uncommitted transaction and h.tx is that
// transaction, so the rows are visible here and roll back with the test.

// rateRows is the counters whose key starts with prefix, as key to count.
func rateRows(t *testing.T, h *harness, prefix string) map[string]int {
	t.Helper()
	rows, err := h.tx.Query(`SELECT key, count FROM rate_limit_counters WHERE key LIKE $1`, prefix+"%")
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		require.NoError(t, rows.Scan(&k, &n))
		out[k] = n
	}
	require.NoError(t, rows.Err())
	return out
}

func requireOneRow(t *testing.T, h *harness, prefix string, wantCount int) {
	t.Helper()
	rows := rateRows(t, h, prefix)
	require.Len(t, rows, 1, "want one counter under %q, have %v", prefix, rows)
	for _, n := range rows {
		require.Equal(t, wantCount, n)
	}
}

func TestRateLimits_AreCountedInTheDatabase(t *testing.T) {
	t.Run("sign-in failures, per account", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		for range 2 {
			res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
				map[string]any{"email": "staff@test.local", "password": "wrong"})
			res.Body.Close()
		}
		requireOneRow(t, h, "auth:login:", 2)
	})

	t.Run("signup, per address", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySelfSignupEnabled, true))
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup", map[string]any{"email": "new@test.local"})
		res.Body.Close()
		requireOneRow(t, h, "auth:signup:", 1)
	})

	t.Run("forgot password, per address", func(t *testing.T) {
		h, cleanup := newHarnessCfg(t, 3, "", emailOn)
		defer cleanup()
		forgot(t, h, "user@test.local")
		requireOneRow(t, h, "auth:forgot-password:", 1)
	})

	t.Run("forgot password mail, per account", func(t *testing.T) {
		h, cleanup := harnessWithEmail(t)
		defer cleanup()
		forgot(t, h, "user@test.local")
		forgot(t, h, "user@test.local")
		require.Len(t, h.resetMail.sent, 1, "one mail per account inside the window")
		requireOneRow(t, h, "password-reset:", 2) // the second request was counted, and refused
	})

	t.Run("guest submission, per address", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
		res := h.doUnauth(t, http.MethodPost, "/api/v1/guest/tickets", map[string]any{})
		res.Body.Close()
		requireOneRow(t, h, "auth:guest-submit:", 1)
	})

	t.Run("reporter_email, per staff member", func(t *testing.T) {
		h, cleanup := newLimitHarness(t)
		defer cleanup()
		res := typedNew(t, h, "staff", 1)
		res.Body.Close()
		requireOneRow(t, h, "on-behalf:"+h.staffID.String(), 1)
	})

	t.Run("guest uploads, per ticket", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tk, token := guestUploadsOn(t, h, 50, 5)
		res := uploadGuestAttachment(t, h, token, "a.txt", []byte("x"))
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		requireOneRow(t, h, "guest-upload:"+tk.ID.String(), 1)
		// An accepted upload gives its refusal unit back.
		requireOneRow(t, h, "guest-refusal:"+tk.ID.String(), 0)
	})

	t.Run("guest link resend, per ticket", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tk, _ := seedGuestTicket(t, h)
		ev := notification.Event{
			Type: notification.EventGuestLinkResent, TrackingNumber: string(tk.TrackingNumber), Recipient: "guest@test.local",
		}
		_, ok, err := h.srv.PrepareGuestLink(context.Background(), ev)
		require.NoError(t, err)
		require.True(t, ok, "the customer who lost their link gets one")
		_, ok, err = h.srv.PrepareGuestLink(context.Background(), ev)
		require.NoError(t, err)
		require.False(t, ok, "a second inside the window must not rotate")
		requireOneRow(t, h, "guest-resend:"+tk.ID.String(), 2)
	})

	t.Run("template test sends, per administrator", func(t *testing.T) {
		h, _, cleanup := harnessWithSMTP(t)
		defer cleanup()
		res := h.doAsAdminSession(t, http.MethodPost, tmplPath+"/ticket_created/test", map[string]any{
			"subject": "Try {{.TrackingNumber}}", "body": "Open {{.TicketURL}}",
		})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		requireOneRow(t, h, "template-test:"+h.adminID.String(), 1)
	})
}

// Each key's window starts at its own first attempt. Under the old shared
// window, ticket B (first used half an hour in) had its count wiped when the
// window rolled at the hour, so it got a fresh budget 30 minutes early, and the
// Retry-After it was given was a promise about the limiter and not about B.
func TestGuestUploadLimits_EachTicketHasItsOwnWindow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	now := time.Now()
	h.srv.SetGuestUploadClockForTest(func() time.Time { return now })
	_, tokenA := guestUploadsOn(t, h, 50, 1)
	_, tokenB := seedGuestTicketFor(t, h, "other@test.local")

	res := uploadGuestAttachment(t, h, tokenA, "a1.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))

	now = now.Add(30 * time.Minute)
	res = uploadGuestAttachment(t, h, tokenB, "b1.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))

	now = now.Add(31 * time.Minute) // 61 minutes in: A's hour is over, B's has 29 minutes to run
	res = uploadGuestAttachment(t, h, tokenA, "a2.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, res.StatusCode, "A's own window ended: "+readBody(t, res))

	res = uploadGuestAttachment(t, h, tokenB, "b2.txt", []byte("x"))
	body := readBody(t, res)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, "B's window has not ended: "+body)
	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err)
	require.InDelta(t, 29*60, secs, 5, "B waits out B's own window")
}

// rateFault is the harness's rate-limit store: the real one until a test sets
// err, and then every call answers that error without touching the database.
//
// Breaking the table instead (renaming it inside the harness transaction) also
// poisons the transaction, so the 500 a test saw came from the NEXT query and
// not from the limiter: a handler that went ahead on a count error passed. An
// injected error is returned by the limiter's own store call and nothing else.
type rateFault struct {
	ratelimit.Store
	err error
}

func (f *rateFault) Take(ctx context.Context, key string, w time.Duration, now time.Time) (ratelimit.Count, error) {
	if f.err != nil {
		return ratelimit.Count{}, f.err
	}
	return f.Store.Take(ctx, key, w, now)
}

func (f *rateFault) Refund(ctx context.Context, key string, windowStart time.Time) error {
	if f.err != nil {
		return f.err
	}
	return f.Store.Refund(ctx, key, windowStart)
}

func (f *rateFault) Spent(ctx context.Context, key string, now time.Time) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.Store.Spent(ctx, key, now)
}

func (f *rateFault) Reset(ctx context.Context, key string) error {
	if f.err != nil {
		return f.err
	}
	return f.Store.Reset(ctx, key)
}

// failRateCounts makes every rate-limit count fail from now on.
func (h *harness) failRateCounts() {
	h.rateFault.err = errors.New("rate limit store: injected failure")
}

// A counter that cannot be written refuses; it does not wave the request
// through. The limits guard mail to strangers and guesses at passwords, so
// "could not check" must not be "go ahead".
func TestRateLimits_FailClosedWhenTheCounterCannotBeUsed(t *testing.T) {
	t.Run("reporter_email is refused", func(t *testing.T) {
		h, cleanup := newLimitHarness(t)
		defer cleanup()
		h.failRateCounts()
		res := typedNew(t, h, "staff", 1)
		body := readBody(t, res)
		// A server fault, not the 429 a refused unit answers: the zero value of
		// a failed count is "not allowed", so ignoring the error would also
		// refuse, with the wrong status and a Retry-After that promises a wait.
		require.Equal(t, http.StatusInternalServerError, res.StatusCode, body)
		require.Equal(t, "internal_error", errCode(t, []byte(body)))
		require.Empty(t, res.Header.Get("Retry-After"))
	})

	t.Run("guest submission is refused", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, true))
		h.failRateCounts()
		res := h.doUnauth(t, http.MethodPost, "/api/v1/guest/tickets", map[string]any{})
		res.Body.Close()
		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	})

	t.Run("a wrong password is not answered as a plain 401", func(t *testing.T) {
		h, cleanup := newHarnessWithRateLimit(t, 3)
		defer cleanup()
		h.failRateCounts()
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "staff@test.local", "password": "wrong"})
		res.Body.Close()
		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	})
}
