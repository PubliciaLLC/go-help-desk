// Package ratelimit is a fixed-window attempt counter whose counts live in the
// database, so they survive a restart and are shared by every replica (#621).
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Count is where an attempt landed.
type Count struct {
	N           int       // attempts in the window, this one included
	WindowStart time.Time // when the key's window opened
	WindowEnd   time.Time // when it ends
}

// Store holds the counts. Take is one atomic step in the store: two attempts
// at once each get their own N, which is what stops both passing the last slot.
type Store interface {
	// Take counts one attempt against key. A key with no live window gets one
	// opened at now, lasting window; a key with one keeps its end.
	Take(ctx context.Context, key string, window time.Duration, now time.Time) (Count, error)
	// Refund gives back one attempt counted in the window that opened at
	// windowStart. Nothing happens if that window is over or the count is zero.
	Refund(ctx context.Context, key string, windowStart time.Time) error
	// Spent is the count in key's live window at now, without counting an attempt.
	Spent(ctx context.Context, key string, now time.Time) (int, error)
	// Reset forgets key.
	Reset(ctx context.Context, key string) error
}

// Limiter permits limit attempts per window for each key, counting in a Store.
//
// The key is supplied by the caller rather than derived from the request,
// because the right key differs per endpoint and only one of them is a network
// address. See the call sites in internal/server/handler_auth.go.
//
// Why not key on the client address, which is the obvious choice: this is
// self-hosted software deployed in topologies we cannot see. Read the address
// from X-Forwarded-For and it is forgeable, so the limit is bypassable and can
// be spent against someone else. Read it from the transport instead and every
// request behind a reverse proxy — which is most Docker deployments — carries
// the proxy's address, so a per-minute budget becomes that budget for the whole
// instance: a 429 outage for an office at 9am. Neither branch is correct
// without operator configuration we cannot verify, so the address is not used
// where a better key exists.
//
// NIST SP 800-63B and RFC 4226 §7.3 both specify throttling per ACCOUNT, not
// per address, which is also the key that needs no configuration to be right.
//
// Every window belongs to one key, opened by that key's first attempt. There is
// no shared reset, so a 429's Retry-After (Taken.RetryAfter) is the wait for
// the key that was refused, not for the limiter as a whole.
//
// An error from the store is returned and never read as an allow: these limits
// guard mail to strangers and guesses at passwords, so a database that cannot
// be asked refuses.
type Limiter struct {
	store  Store
	name   string
	limit  int
	window time.Duration

	// now is the clock; nil means time.Now. Only SetClock sets it, for tests
	// that need to move past a window without sleeping through it.
	now func() time.Time

	// slots holds one queue per over-budget key. Created on first use and
	// emptied as waiters leave, so an instance nobody is attacking carries
	// nothing. In memory on purpose: it is who is waiting in this process right
	// now, not a count, and there is nothing in it to survive a restart.
	mu    sync.Mutex
	slots map[string]*tarpitSlot
}

// New returns a limiter permitting limit attempts per window for a given key.
// A limit of zero disables it. name keeps this limiter's keys apart from every
// other limiter's in the shared store, so two of them can use the same ticket
// ID without sharing a budget.
func New(store Store, name string, limit int, window time.Duration) *Limiter {
	return &Limiter{store: store, name: name, limit: limit, window: window}
}

func (l *Limiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// SetClock replaces the clock. For tests.
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

func (l *Limiter) storeKey(key string) string { return l.name + ":" + key }

// Taken is the outcome of an attempt.
type Taken struct {
	Allowed bool
	// Window identifies the window the attempt was counted in, for Refund.
	Window time.Time
	// RetryAfter is the time until this key's window ends, which is when it may
	// try again. Zero when the limiter is off, never negative.
	RetryAfter time.Duration
}

// Allow records an attempt against key and reports whether it may proceed.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	t, err := l.Take(ctx, key)
	return t.Allowed, err
}

// Take is Allow that also says which window the attempt was counted in, for a
// caller that may need to give it back with Refund, and how long a refused
// caller should wait.
func (l *Limiter) Take(ctx context.Context, key string) (Taken, error) {
	return l.TakeWithLimit(ctx, key, l.limit)
}

// TakeWithLimit is Take with the budget supplied by the caller instead of
// fixed at construction, for a limit an administrator can change while the
// server runs (guest uploads per hour, #445). A limit of zero or less allows
// everything.
func (l *Limiter) TakeWithLimit(ctx context.Context, key string, limit int) (Taken, error) {
	if limit <= 0 {
		return Taken{Allowed: true}, nil
	}
	now := l.clock()
	c, err := l.store.Take(ctx, l.storeKey(key), l.window, now)
	if err != nil {
		return Taken{}, fmt.Errorf("counting an attempt: %w", err)
	}
	return Taken{
		Allowed:    c.N <= limit,
		Window:     c.WindowStart,
		RetryAfter: max(0, c.WindowEnd.Sub(now)),
	}, nil
}

// onceMarkerWindow is how long the record that a row was charged is kept. It
// has to outlast the worker's retries of one row (30 seconds doubling to an
// hour, 8 attempts in all, under three hours) and is swept like any expired
// window.
const onceMarkerWindow = 24 * time.Hour

// AllowOnce is Allow for a send-time budget charged once per queued row, where
// the worker may run the same row several times. onceID identifies the row.
//
// A retry skips the charge only if an earlier attempt of THAT row really made
// it, which is recorded (as a marker key of its own) after the count came back
// allowed. The attempt number cannot say this: the first attempt may have
// failed before it reached the budget, or its count may have failed to write,
// and a retry that skipped on "attempt > 1" went through uncounted. Without a
// marker the retry is counted like any first attempt, and refused if the
// budget is spent.
//
// A row that was charged and allowed is allowed on every retry, whatever has
// been counted since: the budget was spent on it, and refusing it now would
// drop a request half-delivered. If the marker cannot be written the call is an
// error and the retry counts again, which can charge a row twice and never
// lets one through uncounted.
func (l *Limiter) AllowOnce(ctx context.Context, key, onceID string) (bool, error) {
	if l.limit <= 0 {
		return true, nil
	}
	marker := l.storeKey("once:" + onceID + ":" + key)
	now := l.clock()
	charged, err := l.store.Spent(ctx, marker, now)
	if err != nil {
		return false, fmt.Errorf("reading a charge marker: %w", err)
	}
	if charged > 0 {
		return true, nil
	}
	t, err := l.Take(ctx, key)
	if err != nil || !t.Allowed {
		return false, err
	}
	if _, err := l.store.Take(ctx, marker, onceMarkerWindow, now); err != nil {
		return false, fmt.Errorf("recording a charge: %w", err)
	}
	return true, nil
}

// Refund gives back one attempt Take counted against key, for a caller that
// spends before the work and then finds the work did not happen. window is what
// Take returned: if the window has since ended, the attempt was counted in one
// that no longer exists, and giving one back would take a slot the new window
// never lost.
func (l *Limiter) Refund(ctx context.Context, key string, window time.Time) error {
	if l.limit <= 0 {
		return nil
	}
	if err := l.store.Refund(ctx, l.storeKey(key), window); err != nil {
		return fmt.Errorf("refunding an attempt: %w", err)
	}
	return nil
}

// Exceeded reports whether a key has already spent its budget, WITHOUT
// counting an attempt against it.
//
// Allow both counts and decides, which is right where an attempt has already
// happened. This is for asking the question first, so a caller can slow a
// request down before doing the expensive part of it.
func (l *Limiter) Exceeded(ctx context.Context, key string) (bool, error) {
	if l.limit <= 0 {
		return false, nil
	}
	n, err := l.store.Spent(ctx, l.storeKey(key), l.clock())
	if err != nil {
		return false, fmt.Errorf("reading an attempt count: %w", err)
	}
	return n >= l.limit, nil
}

// Reset clears a key's budget, and is called after a successful
// authentication.
//
// NIST SP 800-63B has the verifier disregard prior failed attempts once the
// user authenticates successfully. Without it a user who fumbles a code twice
// and then succeeds is still carrying those failures for the rest of the
// window.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if l.limit <= 0 {
		return nil
	}
	if err := l.store.Reset(ctx, l.storeKey(key)); err != nil {
		return fmt.Errorf("clearing an attempt count: %w", err)
	}
	return nil
}
