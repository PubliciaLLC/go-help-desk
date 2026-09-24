package reputation_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// PolySwarm's ceiling is a shape neither of the other two has: an hourly one.
//
// Restated here as a literal rather than imported from the package, for the
// same reason budget_test.go restates VirusTotal's 500 — a test that reads the
// constant still passes after somebody edits it. 60 an hour is PolySwarm's
// published free-tier figure.
const polySwarmHourly = 60

// Sixty an hour, and the sixtieth goes through.
//
// Both halves matter. A cap that refuses at 59 wastes a lookup an hour
// forever and nobody notices; a cap that permits 61 is a courtesy limit that
// does not limit.
//
// The clock is frozen, which is what makes this an hourly cap rather than a
// daily one in disguise: all 61 spends below happen in the same instant.
func TestBudget_PolySwarmAllowsSixtyLookupsAnHour(t *testing.T) {
	clk := newClock(t, "2026-09-22T10:00:00Z")
	b := clk.budget()

	for i := 0; i < polySwarmHourly; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderPolySwarm),
			"lookup %d of the hour's %d was refused", i+1, polySwarmHourly)
	}

	err := b.Spend(reputation.ProviderPolySwarm)
	require.ErrorIs(t, err, reputation.ErrRateLimited,
		"the 61st lookup in an hour is a throttle that clears within the hour")
	require.NotErrorIs(t, err, reputation.ErrQuotaExceeded,
		"an hour's bucket is not a day's quota, and the retry advice differs")
}

// The bucket is the clock hour, and it resets to a full sixty.
//
// A rolling window would pass a sloppier test than this one and then refuse
// lookups until 10:59 tomorrow. The boundary here is 10:59:59 against
// 11:00:00 — one second apart, different hours.
func TestBudget_PolySwarmsHourResetsOnTheHour(t *testing.T) {
	clk := newClock(t, "2026-09-22T10:00:00Z")
	b := clk.budget()

	for i := 0; i < polySwarmHourly; i++ {
		require.NoError(t, b.Spend(reputation.ProviderPolySwarm))
	}

	clk.set(mustTime(t, "2026-09-22T10:59:59Z"))
	require.ErrorIs(t, b.Spend(reputation.ProviderPolySwarm), reputation.ErrRateLimited,
		"10:59:59 is still the hour whose allowance was just spent")

	clk.set(mustTime(t, "2026-09-22T11:00:00Z"))
	for i := 0; i < polySwarmHourly; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderPolySwarm),
			"spend %d of the new hour's %d was refused; the counter decremented instead of resetting",
			i+1, polySwarmHourly)
	}
	require.ErrorIs(t, b.Spend(reputation.ProviderPolySwarm), reputation.ErrRateLimited)
}

// The hour is the UTC hour, not the hour on the clock the server runs on.
//
// This matters less than the UTC day does — a whole-hour offset resets at the
// same instant either way — but India is +05:30 and Nepal is +05:45, where a
// locally-formatted hour rolls over half an hour early and hands out a second
// allowance the provider has not granted.
func TestBudget_PolySwarmsHourBoundaryIsUTC(t *testing.T) {
	// 15:45 at +05:45 is 10:00 UTC.
	clk := newClock(t, "2026-09-22T15:45:00+05:45")
	b := clk.budget()

	for i := 0; i < polySwarmHourly; i++ {
		require.NoError(t, b.Spend(reputation.ProviderPolySwarm))
	}

	// 16:00 local is 10:15 UTC: a new local hour, the same UTC hour, and the
	// allowance is still spent.
	clk.set(mustTime(t, "2026-09-22T16:00:00+05:45"))
	require.ErrorIs(t, b.Spend(reputation.ProviderPolySwarm), reputation.ErrRateLimited,
		"the bucket resets on the UTC hour, not wherever the server is")

	// 16:45 local is 11:00 UTC: a new UTC hour and a fresh sixty.
	clk.set(mustTime(t, "2026-09-22T16:45:00+05:45"))
	require.NoError(t, b.Spend(reputation.ProviderPolySwarm))
}

// A refused lookup costs nothing, here as everywhere else: 200 attempts in one
// hour grant exactly 60, and the 140 refusals do not come out of the next
// hour's allowance.
func TestBudget_PolySwarmRefusalsAreFree(t *testing.T) {
	clk := newClock(t, "2026-09-22T10:00:00Z")
	b := clk.budget()

	granted := 0
	for i := 0; i < 200; i++ {
		if b.Spend(reputation.ProviderPolySwarm) == nil {
			granted++
		}
	}
	require.Equal(t, polySwarmHourly, granted)

	clk.set(mustTime(t, "2026-09-22T11:00:00Z"))
	granted = 0
	for i := 0; i < 200; i++ {
		if b.Spend(reputation.ProviderPolySwarm) == nil {
			granted++
		}
	}
	require.Equal(t, polySwarmHourly, granted,
		"the previous hour's 140 refusals ate into this hour's allowance")
}

// The three allowances are separate, and neither of the other providers'
// limiters applies to PolySwarm.
//
// The failure this catches is the shape the Budget invites: the per-minute
// bucket is one field and the only thing keeping it VirusTotal's is an `if` on
// the provider name. A third provider added by copying that `if` wrongly would
// cap PolySwarm at four a minute — an hour's sixty would then take fifteen
// minutes to spend and nothing would say why.
func TestBudget_PolySwarmDoesNotShareAnyoneElsesAllowance(t *testing.T) {
	clk := newClock(t, "2026-09-22T10:00:00Z") // frozen: one minute, one hour, one day
	b := clk.budget()

	t.Run("VirusTotal's four a minute is not PolySwarm's", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			require.NoErrorf(t, b.Spend(reputation.ProviderPolySwarm),
				"lookup %d in a single minute was refused; PolySwarm's cap is hourly", i+1)
		}
	})

	t.Run("a spent PolySwarm hour leaves the others whole", func(t *testing.T) {
		for b.Spend(reputation.ProviderPolySwarm) == nil { //nolint:revive // spend it out
		}
		require.NoError(t, b.Spend(reputation.ProviderVirusTotal))
		require.NoError(t, b.Spend(reputation.ProviderMetaDefender))
	})

	t.Run("an exhausted VirusTotal minute leaves PolySwarm whole", func(t *testing.T) {
		clk.set(mustTime(t, "2026-09-22T12:00:00Z"))
		for i := 0; i < 4; i++ {
			require.NoError(t, b.Spend(reputation.ProviderVirusTotal))
		}
		require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrRateLimited)
		require.NoError(t, b.Spend(reputation.ProviderPolySwarm))
	})
}

// The hourly bucket holds under concurrency. A cap that leaks when several
// handlers spend at once holds in every test and fails on a busy instance,
// which is where the ban lives.
//
// Run with -race.
func TestBudget_PolySwarmsHourlyCapHoldsConcurrently(t *testing.T) {
	clk := newClock(t, "2026-09-22T10:00:00Z") // frozen: one hour throughout
	b := clk.budget()

	const attempts = 400
	granted := make(chan struct{}, attempts)
	done := make(chan struct{})
	for w := 0; w < 40; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := w; i < attempts; i += 40 {
				if b.Spend(reputation.ProviderPolySwarm) == nil {
					granted <- struct{}{}
				}
			}
		}(w)
	}
	for w := 0; w < 40; w++ {
		<-done
	}
	close(granted)

	require.Len(t, granted, polySwarmHourly,
		"the hourly bucket leaked under concurrent spends")
}

// A Budget with no injected clock still meters PolySwarm on the real one.
func TestBudget_PolySwarmOnTheRealClock(t *testing.T) {
	var zero reputation.Budget
	require.NoError(t, zero.Spend(reputation.ProviderPolySwarm))
	require.NoError(t, reputation.NewBudget().Spend(reputation.ProviderPolySwarm))
}

// Sanity on the shape of the two limits together: sixty an hour is the only
// ceiling PolySwarm publishes, so spending an hour's worth every hour must
// keep working across a day boundary rather than hitting an invented daily
// cap partway through.
func TestBudget_PolySwarmHasNoInventedDailyCeiling(t *testing.T) {
	start := mustTime(t, "2026-09-22T00:00:00Z")
	clk := &testClock{at: start}
	b := clk.budget()

	for hour := 0; hour < 26; hour++ {
		clk.set(start.Add(time.Duration(hour) * time.Hour))
		for i := 0; i < polySwarmHourly; i++ {
			require.NoErrorf(t, b.Spend(reputation.ProviderPolySwarm),
				"hour %d, lookup %d: refused against a daily ceiling PolySwarm does not publish",
				hour, i+1)
		}
	}
}
