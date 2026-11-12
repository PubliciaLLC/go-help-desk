package reputation

import (
	"fmt"
	"sync"
	"time"
)

// Free-tier ceilings, per provider. Some are figures the provider published
// and some are figures we chose, and which is which is recorded next to each:
// a number of ours, printed as a number of theirs, attributes a commitment to
// them they never made.
//
// VirusTotal publishes 4 requests a minute and 500 a day, resetting at 00:00
// UTC. That one is theirs.
//
// OPSWAT publish NO figure. Their public-API documentation says only "a
// limited number of API calls per day", and 4,000 is OURS — a courtesy cap we
// picked, generous for a help desk and low enough not to become somebody's
// incident, enforced daily because OPSWAT do not throttle single hash lookups
// by the minute. The admin UI says exactly this to the operator (see the
// MetaDefender terms panel in SettingsPage.tsx) and this comment used to
// contradict it. Nobody may treat 4,000 as a published limit, in either
// direction.
//
// PolySwarm publishes neither of those shapes: 60 calls an HOUR, and no daily
// figure at all. Its bucket is therefore the hour, and it has no daily entry
// rather than an invented one — 60x24 would be a number nobody published,
// asserted against somebody else's service.
//
// CIRCL publishes nothing at all: sixty sequential and twenty concurrent
// lookups drew no 429, no rate-limit headers and no documented ceiling, and
// their only statement is that hashlookup is "free and served as a best-effort
// basis". Its entry is therefore OURS rather than theirs — a politeness cap on
// a free service run by a CERT, generous against any real help desk (a ticket
// with ten quarantined attachments spends ten of them) and low enough that a
// loop cannot turn this instance into their problem. Nobody is entitled to
// treat it as a published limit, in either direction.
const (
	virusTotalDailyLimit     = 500
	virusTotalPerMinuteLimit = 4
	metaDefenderDailyLimit   = 4000 // ours, not theirs
	polySwarmPerHourLimit    = 60
	circlPerHourLimit        = 300 // ours, not theirs
)

// Budget is the guard between a page render and somebody else's free tier.
//
// In memory and per process, deliberately: it is a courtesy cap, not an
// accounting record, and a restart spending a handful of extra lookups costs
// nothing. Persisting it would need a table and a migration to protect a
// number nobody reads.
type Budget struct {
	// Now is injected so a daily reset is testable without waiting a day.
	// Nil means time.Now.
	Now func() time.Time

	mu sync.Mutex

	day   string         // UTC date the daily counts belong to, YYYY-MM-DD
	spent map[string]int // lookups spent today, per provider

	minute      string // UTC minute the VirusTotal bucket belongs to
	minuteSpent int

	// The hourly bucket is PER PROVIDER, like the daily one and unlike the
	// per-minute one, which belongs to VirusTotal alone. Two providers are
	// metered by the hour now, and a single shared counter would let a spent
	// CIRCL hour refuse a PolySwarm lookup that was never made.
	hour      string         // UTC hour the hourly buckets belong to, YYYY-MM-DDTHH
	hourSpent map[string]int // lookups spent this hour, per provider
}

// NewBudget returns a Budget on the real clock.
func NewBudget() *Budget { return &Budget{Now: time.Now} }

// Spend takes one lookup from the provider's allowance, or explains why it
// cannot.
//
// The two refusals are different events and the caller must be able to tell
// them apart: ErrRateLimited clears within the minute, ErrQuotaExceeded not
// until 00:00 UTC. Burning a minute's worth of retries against a quota that
// resets tomorrow is the mistake this distinction prevents.
func (b *Budget) Spend(provider string) error {
	// A provider with no ceiling on file gets no allowance at all.
	//
	// Asked as "does this one have a meter" rather than "is this a spelling we
	// accept", and the difference matters for the provider after next. The
	// shapes differ — VirusTotal and MetaDefender publish a daily figure,
	// PolySwarm an hourly one — so neither table alone can stand for "known".
	// Either is enough; NEITHER is refused outright, which is what keeps a
	// fourth provider added to ValidProvider and forgotten here from spending
	// without limit against somebody's free tier.
	dailyCap, hasDaily := dailyLimit(provider)
	hourlyCap, hasHourly := hourlyLimit(provider)
	if !hasDaily && !hasHourly {
		return fmt.Errorf("reputation: unknown provider %q", provider)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	if day := now.Format("2006-01-02"); day != b.day {
		b.day = day
		b.spent = make(map[string]int)
	}
	if hasDaily && b.spent[provider] >= dailyCap {
		return fmt.Errorf("reputation: %s daily budget of %d spent: %w", provider, dailyCap, ErrQuotaExceeded)
	}

	if hasHourly {
		// The hour, not the minute and not the day. Sixty an hour is the only
		// ceiling PolySwarm publishes, and a ticket with ten quarantined
		// attachments spends ten of them in a burst — which the per-hash cache
		// and the refresh interval absorb, and an hourly bucket keeps from
		// ever becoming a ban. CIRCL's hourly figure is one we chose rather
		// than one they publish; see circlPerHourLimit.
		//
		// ErrRateLimited rather than ErrQuotaExceeded: this clears within the
		// hour, and telling a caller to wait for 00:00 UTC instead would cost
		// an operator most of a day of lookups they were entitled to.
		if hour := now.Format("2006-01-02T15"); hour != b.hour {
			b.hour = hour
			b.hourSpent = make(map[string]int)
		}
		if b.hourSpent[provider] >= hourlyCap {
			return fmt.Errorf("reputation: %s allows %d lookups an hour: %w",
				provider, hourlyCap, ErrRateLimited)
		}
		b.hourSpent[provider]++
	}

	if provider == ProviderVirusTotal {
		if minute := now.Format("2006-01-02T15:04"); minute != b.minute {
			b.minute = minute
			b.minuteSpent = 0
		}
		if b.minuteSpent >= virusTotalPerMinuteLimit {
			return fmt.Errorf("reputation: %s allows %d lookups a minute: %w",
				provider, virusTotalPerMinuteLimit, ErrRateLimited)
		}
		b.minuteSpent++
	}

	b.spent[provider]++
	return nil
}

func (b *Budget) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

// dailyLimit is the provider's daily ceiling, and whether it has one. False
// means "no daily cap to enforce", not "unknown provider": Spend refuses a
// provider that has neither this nor an hourly limit.
//
// "Published" would be wrong for half of this table: VirusTotal's 500 is their
// figure, MetaDefender's 4,000 is ours. See the constants for which is which.
func dailyLimit(provider string) (int, bool) {
	switch provider {
	case ProviderVirusTotal:
		return virusTotalDailyLimit, true
	case ProviderMetaDefender:
		return metaDefenderDailyLimit, true
	}
	return 0, false
}

// hourlyLimit is the same for a provider whose ceiling is by the hour instead.
// A provider may have both, and a provider with neither is refused.
//
// CIRCL is here despite publishing no limit at all, which is the point: the
// alternative to a cap we chose is no cap, and no cap against a free
// best-effort service is how an instance becomes somebody's incident. See the
// constant.
func hourlyLimit(provider string) (int, bool) {
	switch provider {
	case ProviderPolySwarm:
		return polySwarmPerHourLimit, true
	case ProviderCIRCL:
		return circlPerHourLimit, true
	}
	return 0, false
}
