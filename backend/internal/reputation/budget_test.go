package reputation_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// The free-tier ceilings, restated here rather than read from the package.
//
// They are unexported constants, and that is the right place for them, but a
// test that imported them would assert nothing: "the cap equals the cap" still
// passes after somebody edits the constant to 5,000. These literals are the
// numbers VirusTotal and OPSWAT publish, so changing one of them has to be a
// deliberate edit in two places with a public quota page to justify it.
const (
	virusTotalDaily     = 500
	virusTotalPerMinute = 4
	metaDefenderDaily   = 4000
)

// ---------------------------------------------------------------------------
// The injected clock.
//
// Budget.Now exists so that a daily reset is testable without waiting a day.
// Every test below moves this clock instead of sleeping; nothing here is
// timing-dependent and nothing takes longer than the arithmetic.
// ---------------------------------------------------------------------------

type testClock struct {
	mu sync.Mutex
	at time.Time
}

// newClock parses an RFC3339 instant, offset and all: the zone is load-bearing
// in the UTC-boundary test below.
func newClock(t *testing.T, rfc3339 string) *testClock {
	t.Helper()
	return &testClock{at: mustTime(t, rfc3339)}
}

func mustTime(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, rfc3339)
	require.NoError(t, err)
	return at
}

// now is guarded because Budget calls it while holding its own lock, and the
// concurrency tests read it from many goroutines at once. It takes only this
// clock's lock, never the Budget's, so the two cannot deadlock.
func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// budget returns a Budget on this clock.
func (c *testClock) budget() *reputation.Budget {
	return &reputation.Budget{Now: c.now}
}

// ---------------------------------------------------------------------------
// The daily cap.
// ---------------------------------------------------------------------------

// Each provider is capped at its OWN published ceiling, and the last permitted
// spend goes through.
//
// Both halves matter. A cap that refuses at 499 wastes a lookup a day forever
// and no one notices; a cap that permits 501 is a courtesy limit that does not
// limit. Asserting only "the 501st is refused" passes against an off-by-one in
// either direction.
//
// The tick is why the two rows differ: VirusTotal also allows only four
// lookups a minute, so spending its 500 takes 500 distinct minutes of clock.
// MetaDefender has no per-minute limit and spends its 4,000 on a frozen clock,
// which is itself part of what the MetaDefender row pins.
func TestBudget_DailyCapIsThePerProviderCeiling(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		limit    int
		tick     time.Duration
	}{
		{"virustotal", reputation.ProviderVirusTotal, virusTotalDaily, time.Minute},
		{"metadefender", reputation.ProviderMetaDefender, metaDefenderDaily, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := newClock(t, "2025-09-22T00:00:00Z")
			b := clk.budget()
			start := clk.now()

			for i := 0; i < tc.limit; i++ {
				clk.set(start.Add(time.Duration(i) * tc.tick))
				require.NoErrorf(t, b.Spend(tc.provider),
					"spend %d of the day's %d was refused", i+1, tc.limit)
			}

			clk.set(start.Add(time.Duration(tc.limit) * tc.tick))
			err := b.Spend(tc.provider)
			require.ErrorIsf(t, err, reputation.ErrQuotaExceeded,
				"spend %d must be refused, and as a quota that clears at 00:00 UTC", tc.limit+1)
			// The two refusals are different events with different retry
			// advice. A caller that reads a spent day as a minute's throttle
			// retries against it all afternoon.
			require.NotErrorIs(t, err, reputation.ErrRateLimited,
				"a spent day is not a minute's throttle")
		})
	}
}

// The counter resets at 00:00 UTC, and it resets to zero.
//
// The boundary is the point. A spend at 23:59:59 and a spend at 00:00:00 are
// one second apart and belong to different days; an implementation that keeps
// a rolling twenty-four hours passes a sloppier test than this one and then
// refuses lookups until 23:59:59 tomorrow.
//
// The second half — spending the whole allowance again on the new day — is
// what separates a reset from a decrement.
func TestBudget_DailyCounterResetsAtMidnightUTC(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		limit    int
		tick     time.Duration
	}{
		{"virustotal", reputation.ProviderVirusTotal, virusTotalDaily, time.Minute},
		{"metadefender", reputation.ProviderMetaDefender, metaDefenderDaily, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Lay the whole allowance down so the last spend lands at
			// 23:59:00 on 22 September.
			lastSpend := mustTime(t, "2025-09-22T23:59:00Z")
			day1 := lastSpend.Add(-time.Duration(tc.limit-1) * tc.tick)

			clk := &testClock{at: day1}
			b := clk.budget()
			for i := 0; i < tc.limit; i++ {
				clk.set(day1.Add(time.Duration(i) * tc.tick))
				require.NoErrorf(t, b.Spend(tc.provider), "spend %d of %d", i+1, tc.limit)
			}

			// One second before midnight: the same UTC day, and it is spent.
			clk.set(mustTime(t, "2025-09-22T23:59:59Z"))
			require.ErrorIs(t, b.Spend(tc.provider), reputation.ErrQuotaExceeded,
				"23:59:59 is still the day whose allowance was just spent")

			// One second later. A rolling twenty-four hours refuses this.
			day2 := mustTime(t, "2025-09-23T00:00:00Z")
			clk.set(day2)
			require.NoError(t, b.Spend(tc.provider),
				"00:00:00 UTC is a new day and a fresh allowance")

			// And the fresh allowance is the whole allowance: limit-1 more
			// spends go through, and only then is the day spent again.
			for i := 1; i < tc.limit; i++ {
				clk.set(day2.Add(time.Duration(i) * tc.tick))
				require.NoErrorf(t, b.Spend(tc.provider),
					"spend %d of the new day's %d was refused; the counter decremented instead of resetting",
					i+1, tc.limit)
			}
			clk.set(day2.Add(time.Duration(tc.limit) * tc.tick))
			require.ErrorIs(t, b.Spend(tc.provider), reputation.ErrQuotaExceeded)
		})
	}
}

// The day is the UTC day, not the day on the clock the process happens to be
// running on. Both providers reset their quotas at 00:00 UTC regardless of
// where the server is.
//
// The clock here is two hours ahead, so its local date flips two hours before
// the quota does. An implementation that formats the time it is handed rather
// than its UTC instant resets at the wrong moment — and in this direction it
// hands out a second full allowance before the provider does, which is the
// failure that gets an instance's key banned rather than merely throttled.
func TestBudget_TheDayBoundaryIsUTCNotTheClocksZone(t *testing.T) {
	// 01:30 on the 23rd at +02:00 is 23:30 UTC on the 22nd.
	clk := newClock(t, "2025-09-23T01:30:00+02:00")
	b := clk.budget()

	for i := 0; i < metaDefenderDaily; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderMetaDefender), "spend %d", i+1)
	}

	// 01:59 local is 23:59 UTC: the same UTC day, still spent. A local-day
	// implementation has already reset here too, so this alone does not catch
	// it — the next assertion does.
	clk.set(mustTime(t, "2025-09-23T01:59:00+02:00"))
	require.ErrorIs(t, b.Spend(reputation.ProviderMetaDefender), reputation.ErrQuotaExceeded)

	// 02:00 local is 00:00 UTC on the 23rd: a new UTC day, and a new
	// allowance. On the clock's own calendar it is still the 23rd, the same
	// date every spend above was made on, so a local-day implementation
	// refuses this.
	clk.set(mustTime(t, "2025-09-23T02:00:00+02:00"))
	require.NoError(t, b.Spend(reputation.ProviderMetaDefender),
		"the quota resets at 00:00 UTC, not at midnight wherever the server is")
}

// ---------------------------------------------------------------------------
// The per-minute bucket. VirusTotal only.
// ---------------------------------------------------------------------------

// VirusTotal publishes four requests a minute. The fifth in a minute is
// refused, and refused as a throttle rather than as a spent day, because the
// retry advice is a minute rather than a wait for 00:00 UTC.
func TestBudget_VirusTotalAllowsFourLookupsAMinute(t *testing.T) {
	clk := newClock(t, "2025-09-22T10:00:00Z")
	b := clk.budget()

	for i := 0; i < virusTotalPerMinute; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderVirusTotal), "lookup %d of the minute's four", i+1)
	}

	err := b.Spend(reputation.ProviderVirusTotal)
	require.ErrorIs(t, err, reputation.ErrRateLimited, "the fifth lookup in a minute is a throttle")
	require.NotErrorIs(t, err, reputation.ErrQuotaExceeded,
		"496 of the day's 500 are still unspent; this clears in seconds, not at midnight")

	// Still the same minute 59 seconds later.
	clk.advance(59 * time.Second)
	require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrRateLimited)

	// And the next minute is a fresh four.
	clk.advance(time.Second)
	for i := 0; i < virusTotalPerMinute; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderVirusTotal), "lookup %d of the next minute's four", i+1)
	}
	require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrRateLimited)
}

// A refusal costs nothing. Twenty lookups turned away by the minute bucket
// must not quietly come out of the day's 500.
//
// This is the arithmetic that makes the distinction between the two refusals
// worth having: a busy minute on a ticket page would otherwise burn a day's
// quota without performing a single lookup, and the only symptom would be
// "not checked yet" on every attachment for the rest of the day.
func TestBudget_ARefusedLookupDoesNotSpendTheDailyAllowance(t *testing.T) {
	clk := newClock(t, "2025-09-22T00:00:00Z")
	b := clk.budget()
	start := clk.now()

	// Hammer the first minute: four go through, twenty are turned away.
	granted := 0
	for i := 0; i < 24; i++ {
		if b.Spend(reputation.ProviderVirusTotal) == nil {
			granted++
		}
	}
	require.Equal(t, virusTotalPerMinute, granted, "the first minute granted the wrong number")

	// Then take four a minute until the day runs out, counting.
	for minute := 1; ; minute++ {
		require.Less(t, minute, 2*virusTotalDaily, "the daily allowance never ran out")
		clk.set(start.Add(time.Duration(minute) * time.Minute))

		spent := false
		for i := 0; i < virusTotalPerMinute; i++ {
			err := b.Spend(reputation.ProviderVirusTotal)
			if errors.Is(err, reputation.ErrQuotaExceeded) {
				spent = true
				break
			}
			require.NoError(t, err)
			granted++
		}
		if spent {
			break
		}
	}

	require.Equal(t, virusTotalDaily, granted,
		"the day granted %d lookups, not %d: the twenty rate-limited refusals ate into the daily allowance",
		granted, virusTotalDaily)
}

// MetaDefender has no per-minute limit. OPSWAT does not throttle single hash
// lookups, so a hundred in one minute is fine — it is the daily 4,000 that
// eventually stops it.
//
// This fails the moment somebody applies VirusTotal's bucket to both, which is
// the shape the code invites: the bucket is one field on the Budget and the
// only thing keeping it VirusTotal's is one `if` on the provider name.
func TestBudget_MetaDefenderHasNoPerMinuteBucket(t *testing.T) {
	clk := newClock(t, "2025-09-22T10:00:00Z") // frozen: every spend below is in one minute
	b := clk.budget()

	for i := 0; i < 100; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderMetaDefender),
			"lookup %d in a single minute was refused; MetaDefender is not throttled per minute", i+1)
	}
}

// ---------------------------------------------------------------------------
// The two allowances are separate.
// ---------------------------------------------------------------------------

// One provider's budget is not the other's. An instance that switches provider
// mid-day must not find the new one already spent, and a shared counter would
// also cap VirusTotal at whatever MetaDefender had used.
func TestBudget_ProvidersDoNotShareAnAllowance(t *testing.T) {
	t.Run("a spent virustotal day leaves metadefender whole", func(t *testing.T) {
		clk := newClock(t, "2025-09-22T00:00:00Z")
		b := clk.budget()
		start := clk.now()

		for i := 0; i < virusTotalDaily; i++ {
			clk.set(start.Add(time.Duration(i) * time.Minute))
			require.NoError(t, b.Spend(reputation.ProviderVirusTotal))
		}
		require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrQuotaExceeded)

		require.NoError(t, b.Spend(reputation.ProviderMetaDefender),
			"VirusTotal's spent day is not MetaDefender's")
	})

	t.Run("a spent metadefender day leaves virustotal whole", func(t *testing.T) {
		clk := newClock(t, "2025-09-22T10:00:00Z")
		b := clk.budget()

		for i := 0; i < metaDefenderDaily; i++ {
			require.NoError(t, b.Spend(reputation.ProviderMetaDefender))
		}
		require.ErrorIs(t, b.Spend(reputation.ProviderMetaDefender), reputation.ErrQuotaExceeded)

		require.NoError(t, b.Spend(reputation.ProviderVirusTotal),
			"MetaDefender's spent day is not VirusTotal's")
	})

	t.Run("the minute bucket is virustotal's alone", func(t *testing.T) {
		clk := newClock(t, "2025-09-22T10:00:00Z") // frozen
		b := clk.budget()

		for i := 0; i < virusTotalPerMinute; i++ {
			require.NoError(t, b.Spend(reputation.ProviderVirusTotal))
		}
		require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrRateLimited)

		// Same minute, same Budget, other provider.
		require.NoError(t, b.Spend(reputation.ProviderMetaDefender),
			"VirusTotal's exhausted minute must not throttle MetaDefender")
	})
}

// A provider name the Budget does not recognise is refused outright, and
// refused as neither of the two sentinels.
//
// The failure this prevents is an unmetered allowance: a typo in the
// attachment_reputation_provider setting, or a third provider added without a
// ceiling, would otherwise spend without limit against somebody's free tier.
// The error also has to name the value, because "unknown provider" without it
// is undebuggable from a log line.
func TestBudget_AnUnknownProviderGetsNoAllowance(t *testing.T) {
	b := newClock(t, "2025-09-22T10:00:00Z").budget()

	err := b.Spend("virus-total")
	require.Error(t, err, "an unknown provider must not be granted an unmetered lookup")
	require.NotErrorIs(t, err, reputation.ErrQuotaExceeded)
	require.NotErrorIs(t, err, reputation.ErrRateLimited)
	require.Contains(t, err.Error(), "virus-total", "the reason has to name the value that was wrong")
}

// A Budget with no clock injected still works, on the real one. NewBudget sets
// it; a zero value does not, and a nil func here would panic on the first
// lookup of the process rather than in any test.
func TestBudget_NilClockFallsBackToTheRealOne(t *testing.T) {
	var zero reputation.Budget
	require.NoError(t, zero.Spend(reputation.ProviderMetaDefender))
	require.NoError(t, reputation.NewBudget().Spend(reputation.ProviderVirusTotal))
}

// ---------------------------------------------------------------------------
// Concurrency.
//
// Budget is mutex-guarded and every ticket page render goes through it, so the
// caps have to hold when several handlers spend at once. A cap that leaks
// under concurrency is worse than no cap: it holds in every test and fails on
// a busy instance, which is where the ban lives.
//
// Run with -race.
// ---------------------------------------------------------------------------

func TestBudget_ConcurrentSpendsNeverExceedTheCap(t *testing.T) {
	// spendInParallel makes attempts spends across 60 goroutines and returns
	// how many were granted plus every refusal.
	spendInParallel := func(b *reputation.Budget, provider string, attempts int) (int64, []error) {
		const workers = 60
		var granted atomic.Int64
		refusals := make(chan error, attempts)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := w; i < attempts; i += workers {
					if err := b.Spend(provider); err != nil {
						refusals <- err
					} else {
						granted.Add(1)
					}
				}
			}(w)
		}
		wg.Wait()
		close(refusals)

		var errs []error
		for err := range refusals {
			errs = append(errs, err)
		}
		return granted.Load(), errs
	}

	t.Run("the daily cap holds", func(t *testing.T) {
		clk := newClock(t, "2025-09-22T10:00:00Z") // frozen: one UTC day throughout
		b := clk.budget()

		granted, refusals := spendInParallel(b, reputation.ProviderMetaDefender, metaDefenderDaily+2000)

		require.Equal(t, int64(metaDefenderDaily), granted,
			"the cap leaked: %d lookups were granted against a ceiling of %d", granted, metaDefenderDaily)
		require.Len(t, refusals, 2000)
		for _, err := range refusals {
			require.ErrorIs(t, err, reputation.ErrQuotaExceeded)
		}
	})

	t.Run("the minute bucket holds", func(t *testing.T) {
		clk := newClock(t, "2025-09-22T10:00:00Z") // frozen: one minute throughout
		b := clk.budget()

		granted, refusals := spendInParallel(b, reputation.ProviderVirusTotal, 200)

		require.Equal(t, int64(virusTotalPerMinute), granted,
			"the minute bucket leaked: %d lookups were granted against a ceiling of %d",
			granted, virusTotalPerMinute)
		require.Len(t, refusals, 200-virusTotalPerMinute)
		for _, err := range refusals {
			require.ErrorIs(t, err, reputation.ErrRateLimited,
				"196 lookups turned away in one minute is a throttle, not a spent day")
		}
	})
}
