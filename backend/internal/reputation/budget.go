package reputation

import (
	"fmt"
	"sync"
	"time"
)

// Free-tier ceilings, per provider.
//
// VirusTotal publishes 4 requests a minute and 500 a day; OPSWAT publishes
// 4,000 a day and does not throttle single hash lookups, so it needs the daily
// counter and no bucket. Both quotas reset at 00:00 UTC.
const (
	virusTotalDailyLimit     = 500
	virusTotalPerMinuteLimit = 4
	metaDefenderDailyLimit   = 4000
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
	limit, ok := dailyLimit(provider)
	if !ok {
		return fmt.Errorf("reputation: unknown provider %q", provider)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()

	if day := now.Format("2006-01-02"); day != b.day {
		b.day = day
		b.spent = make(map[string]int)
	}
	if b.spent[provider] >= limit {
		return fmt.Errorf("reputation: %s daily budget of %d spent: %w", provider, limit, ErrQuotaExceeded)
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

func dailyLimit(provider string) (int, bool) {
	switch provider {
	case ProviderVirusTotal:
		return virusTotalDailyLimit, true
	case ProviderMetaDefender:
		return metaDefenderDailyLimit, true
	}
	return 0, false
}
