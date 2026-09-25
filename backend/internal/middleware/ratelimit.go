package middleware

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a fixed-window counter over an arbitrary key.
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
// INTERIM: state is in memory and per process, so a restart clears every
// counter and N replicas multiply every budget by N. A patient attacker who
// already holds a password can still work through a meaningful share of the
// TOTP space over weeks. The durable fix is a failed-attempt count on the user
// row with an escalating time lock — tracked separately; this closes the
// immediate hole.
type RateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	counts  map[string]int
	resetAt time.Time

	// slots holds one queue per over-budget key. Created on first use and
	// emptied as waiters leave, so an instance nobody is attacking carries
	// nothing.
	slots map[string]*tarpitSlot
}

// NewRateLimiter returns a limiter permitting limit attempts per window for a
// given key. A limit of zero disables it.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		counts:  make(map[string]int),
		resetAt: time.Now().Add(window),
	}
}

// Allow records an attempt against key and reports whether it may proceed.
func (rl *RateLimiter) Allow(key string) bool {
	if rl.limit <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.After(rl.resetAt) {
		// Whole-map reset rather than per-key expiry: it bounds memory without
		// a sweeper, which matters because keys are partly attacker-chosen
		// (any email address can be submitted).
		rl.counts = make(map[string]int)
		rl.resetAt = now.Add(rl.window)
	}
	rl.counts[key]++
	return rl.counts[key] <= rl.limit
}

// Exceeded reports whether a key has already spent its budget, WITHOUT
// counting an attempt against it.
//
// Allow both counts and decides, which is right where an attempt has already
// happened. This is for asking the question first, so a caller can slow a
// request down before doing the expensive part of it.
func (rl *RateLimiter) Exceeded(key string) bool {
	if rl.limit <= 0 {
		return false
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if time.Now().After(rl.resetAt) {
		return false
	}
	return rl.counts[key] >= rl.limit
}

// Tarpit slows an over-budget key down. It never refuses one.
//
// Password verification runs before the counter is consulted — deliberately,
// so that a correct password is always honoured and nobody can be locked out
// of their own account by somebody else's guessing — so the counter alone only
// changed the status code on a wrong guess. An attacker who ignored 429 had
// unlimited online guesses, bounded by bcrypt alone.
//
// What this buys, measured through the real login handler on a ten-core
// machine with a one-second delay:
//
//	connections   with tarpit   without
//	          1        ~1/sec    22/sec
//	          8         9/sec   132/sec
//	        256       154/sec   154/sec
//
// So it costs a serial client about twenty times, and against something that
// opens a couple of hundred connections it costs nothing at all — at that
// point bcrypt is the only thing left, which is where this started. The bound
// is connections divided by the delay, not one over the delay, and saying
// otherwise would be describing a control that is not there. MFA is the
// answer to a determined parallel attacker; this is the answer to a script.
//
// Two rules, and the second is why the number above is not better.
//
// One turn at a time per key, so a serial caller is slowed by the delay
// rather than just made to wait once.
//
// And nobody waits longer than twice the delay. The first version bounded the
// queue instead and refused anything past it, which handed an attacker the
// lockout this ordering exists to prevent: eight connections against a known
// email address, and the owner's CORRECT password came back 429. A request
// that cannot get its turn in time now goes ahead without one. That is what
// flattens the table above at high concurrency, and it is the right trade:
// the guarantee that nobody can be kept out of their own account is worth
// more than a bound that a determined attacker walks around anyway.
func (rl *RateLimiter) Tarpit(ctx context.Context, key string, delay time.Duration) {
	if rl.limit <= 0 || delay <= 0 {
		return
	}

	rl.mu.Lock()
	if rl.slots == nil {
		rl.slots = make(map[string]*tarpitSlot)
	}
	slot, ok := rl.slots[key]
	if !ok {
		slot = &tarpitSlot{turn: make(chan struct{}, 1)}
		rl.slots[key] = slot
	}
	slot.waiting++
	rl.mu.Unlock()

	// Dropped once the last waiter leaves. Keys are partly attacker-chosen --
	// any email address can be submitted -- so a map that only grows is a leak.
	defer func() {
		rl.mu.Lock()
		slot.waiting--
		if slot.waiting == 0 {
			delete(rl.slots, key)
		}
		rl.mu.Unlock()
	}()

	wait := time.NewTimer(delay)
	defer wait.Stop()

	select {
	case slot.turn <- struct{}{}:
		// Our turn. Hold it for the delay, then hand it on.
		defer func() { <-slot.turn }()
	case <-wait.C:
		// Somebody else has it and our patience is spent. Going ahead
		// unthrottled is the deliberate choice; see above.
		return
	case <-ctx.Done():
		return
	}

	hold := time.NewTimer(delay)
	defer hold.Stop()
	select {
	case <-hold.C:
	case <-ctx.Done():
	}
}

// tarpitSlot is one key's queue: a one-deep channel that makes the waits
// sequential, and a count so the slot can be freed when it empties.
type tarpitSlot struct {
	turn    chan struct{}
	waiting int
}

// Reset clears a key's budget, and is called after a successful
// authentication.
//
// NIST SP 800-63B has the verifier disregard prior failed attempts once the
// user authenticates successfully. Without it a user who fumbles a code twice
// and then succeeds is still carrying those failures for the rest of the
// window.
func (rl *RateLimiter) Reset(key string) {
	if rl.limit <= 0 {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.counts, key)
}

// ClientAddr is the transport address, with the port stripped.
//
// Only for endpoints where no account key exists yet — signup. It is
// deliberately NOT read from X-Forwarded-For: that header is attacker
// controlled unless a trusted proxy is known to rewrite it, and honouring it
// would let one client mint a fresh identity per request.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
