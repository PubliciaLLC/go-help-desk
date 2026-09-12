package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a fixed-window counter keyed by client address.
//
// It exists because credential endpoints had no throttle at all: a six-digit
// TOTP is about 500,000 guesses on average and totp.Validate accepts a ±1
// window, so three windows' codes are live at once. Unthrottled, that is
// brute-forceable in under an hour. Login had only bcrypt slowing it down.
//
// In-memory and per-process on purpose. This is a single-binary self-hosted
// help desk, so a shared store would mean a new dependency for a guard that is
// useful the moment it exists. Behind several replicas each process enforces
// its own share, which is weaker than a shared counter but strictly better
// than nothing — and this is a speed bump, not an access control.
type RateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	counts  map[string]int
	resetAt time.Time
}

// NewRateLimiter returns a limiter permitting limit requests per window from
// one address. A limit of zero disables it, which is what the test harness
// uses — a suite that logs in hundreds of times in a second is not an attack.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		counts:  make(map[string]int),
		resetAt: time.Now().Add(window),
	}
}

// allow reports whether this key may proceed, counting the attempt.
func (rl *RateLimiter) allow(key string, now time.Time) bool {
	if rl.limit <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if now.After(rl.resetAt) {
		// Whole-map reset rather than per-key expiry: it keeps the structure
		// from growing without bound under a spray of forged addresses, which
		// a per-key TTL map would not.
		rl.counts = make(map[string]int)
		rl.resetAt = now.Add(rl.window)
	}
	rl.counts[key]++
	return rl.counts[key] <= rl.limit
}

// Middleware throttles the routes it wraps.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientKey(r), time.Now()) {
			// Same envelope shape the rest of the chain emits.
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":{"code":"rate_limited","message":"too many attempts; please wait a minute and try again"}}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller.
//
// RemoteAddr only: X-Forwarded-For is attacker-controlled unless a trusted
// proxy is known to rewrite it, and trusting it here would let one client
// forge a fresh identity per request and bypass the limit entirely. Behind a
// reverse proxy this throttles per proxy, which is the safe failure direction —
// too strict rather than useless. Revisit alongside a trusted-proxy setting.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
