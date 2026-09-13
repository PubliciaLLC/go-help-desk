package middleware

import (
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
