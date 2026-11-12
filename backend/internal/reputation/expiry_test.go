package reputation_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// Verdicts go stale, so they expire — and staff can always ask again (#168).
//
// Two rules, both reading the same field. A stored verdict older than the
// configured interval is re-fetched on the next render; a person looking at
// one can ask for a re-check by hand at most once a week. Both are "is this
// older than N", so every test here moves an injected clock instead of
// sleeping: a test that passes because the row was written two seconds ago
// proves nothing about a row written a fortnight ago.
//
// The fakes, the frozen clock and the fixtures are the ones in service_test.go
// and budget_test.go.

const (
	expiryDay      = 24 * time.Hour
	expiryBiweekly = 14 * expiryDay
)

// newExpiringService wires a service and its budget to one clock, so that
// moving that clock moves both. Nothing here sleeps.
func newExpiringService(t *testing.T, clk *testClock, prov *fakeProvider, store *fakeStore,
	refreshAfter time.Duration) (*reputation.Service, *reputation.Budget) {
	t.Helper()
	b := clk.budget()
	svc := reputation.NewService(prov, store, b)
	svc.Now = clk.now
	svc.RefreshAfter = refreshAfter
	return svc, b
}

// fetchedAgo is the verdict as the store hands it back: with the time WE
// fetched it on board, which is the only thing either rule can be decided on.
func fetchedAgo(clk *testClock, rep reputation.Reputation, ago time.Duration) reputation.Reputation {
	rep.FetchedAt = clk.now().Add(-ago)
	return rep
}

// ---------------------------------------------------------------------------
// Automatic expiry.
// ---------------------------------------------------------------------------

// Inside the interval nothing changes: the stored verdict is still the answer,
// and it costs neither a request nor a lookup from the allowance.
//
// This is the half that keeps the lazy design affordable. An implementation
// that re-fetches whenever it sees a fetched_at it does not like turns one
// lookup per hash per fortnight into one per render.
func TestService_AVerdictInsideTheIntervalIsStillTheAnswer(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, cleanVerdict(), 13*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	// Primed with a different verdict, so an implementation that looks up
	// anyway cannot pass by returning something plausible.
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, stored, got)
	require.Empty(t, prov.calls(), "13 days old under a 14 day interval is not stale")
	require.Empty(t, store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// Past the interval the verdict is re-fetched, kept, and the fresh answer is
// what the caller gets.
//
// The stored verdict says clean and the provider now says detected, which is
// the case the whole feature exists for: new signatures catch old malware, and
// a sample nobody had submitted a fortnight ago is precisely the one that gets
// submitted later.
func TestService_AVerdictPastTheIntervalIsRefetched(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, fetchedAgo(clk, cleanVerdict(), 15*expiryDay))

	fresh := detectedVerdict()
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: fresh}
	svc, budget := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, fresh, got, "the new answer, not the one it replaced")
	require.Equal(t, []string{eicarSHA}, prov.calls())
	require.Equal(t,
		[]putRecord{{sha256: eicarSHA, provider: reputation.ProviderVirusTotal, rep: fresh}},
		store.written(), "a re-check that is not written re-checks again on the next render")
	requireBudgetLeft(t, budget, virusTotalPerMinute-1)
}

// The boundary itself, stated rather than left to whoever reads the code next:
// at exactly the interval the verdict has reached its age and is re-fetched.
func TestService_TheIntervalBoundaryIsInclusive(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, fetchedAgo(clk, cleanVerdict(), expiryBiweekly))

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, expiryBiweekly)

	_, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Len(t, prov.calls(), 1, "a verdict exactly as old as the interval is stale")
}

// A detection never expires, however old it gets.
//
// Engines do not un-flag a file, and re-confirming known malware is the one
// lookup guaranteed to tell nobody anything. A 500-a-day allowance spent on it
// is an allowance not spent on the files whose answer might have changed.
func TestService_ADetectionNeverExpires(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, detectedVerdict(), 400*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, 7*expiryDay)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, stored, got)
	require.Empty(t, prov.calls(), "a detection from over a year ago was re-checked for nothing")
	require.Empty(t, store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// "never" means never: an interval of zero switches automatic re-checking off
// rather than making everything stale.
//
// The inverted reading is the dangerous one — zero compared against an age
// makes every verdict expired on every render, and an operator who set this to
// save quota would have set fire to it instead.
func TestService_NeverMeansNoAutomaticRefetch(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, cleanVerdict(), 400*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, 0)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, stored, got)
	require.Empty(t, prov.calls())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// A verdict whose age we do not know is not re-fetched.
//
// A store that does not record when it fetched cannot support expiry, and
// reading its zero time as "written in year one" would re-fetch every verdict
// on every render — the exact allowance-burning failure the cache exists to
// prevent. Unknown age is a reason not to spend, not a reason to spend.
func TestService_AVerdictWithNoFetchTimeIsNotStale(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := cleanVerdict() // no FetchedAt at all
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, stored, got)
	require.Empty(t, prov.calls(), "an unknown fetch time is not evidence the verdict is old")
}

// A re-check that cannot be made leaves the stale verdict on screen.
//
// The alternative — reporting Unavailable because the refresh failed — deletes
// a real answer from the page the moment it passes its interval and the
// allowance is gone. "Clean, checked three weeks ago" is worth more to the
// person reading it than "not checked yet", and the row carries the date so
// they can judge it.
func TestService_AFailedRefetchKeepsTheStoredVerdict(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, cleanVerdict(), 30*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, expiryBiweekly)

	// The minute's allowance is gone before the render.
	for i := 0; i < virusTotalPerMinute; i++ {
		require.NoError(t, budget.Spend(reputation.ProviderVirusTotal))
	}

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.ErrorIs(t, err, reputation.ErrRateLimited, "the operator still gets the reason")
	require.Equal(t, stored, got, "a stale verdict is still a verdict")
	require.Empty(t, prov.calls())
	require.Empty(t, store.written())
}

// ---------------------------------------------------------------------------
// The manual re-check.
// ---------------------------------------------------------------------------

// Staff can ask again, and the answer replaces the stored one.
//
// The setting here is "never", which is the case that matters: an operator who
// turned automatic checking off to save quota did not mean "nobody may ever
// ask". The floor is independent of the setting in both directions.
func TestService_RefreshAsksAgainEvenWhenTheSettingIsNever(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, fetchedAgo(clk, cleanVerdict(), 8*expiryDay))

	fresh := detectedVerdict()
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: fresh}
	svc, budget := newExpiringService(t, clk, prov, store, 0)

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, fresh, got)
	require.Equal(t, []string{eicarSHA}, prov.calls())
	require.Equal(t,
		[]putRecord{{sha256: eicarSHA, provider: reputation.ProviderVirusTotal, rep: fresh}},
		store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute-1)
}

// At most once a week per hash, whatever the setting says.
//
// A floor rather than an override: a bored reader with a button cannot spend
// an operator's allowance in a loop. Seven days is short enough to be useful
// and long enough that the loop is pointless.
func TestService_RefreshIsRefusedInsideTheWeeklyFloor(t *testing.T) {
	cases := []struct {
		name string
		ago  time.Duration
	}{
		{"asked again immediately", 0},
		{"asked again the next day", expiryDay},
		{"asked again an hour short of the week", 7*expiryDay - time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := newClock(t, "2026-09-23T10:00:00Z")
			store := newFakeStore()
			stored := fetchedAgo(clk, cleanVerdict(), tc.ago)
			store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

			prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
			// A weekly setting, so the automatic rule cannot be what refuses
			// this: only the floor can.
			svc, budget := newExpiringService(t, clk, prov, store, 7*expiryDay)

			got, err := svc.Refresh(context.Background(), eicarSHA)
			require.ErrorIs(t, err, reputation.ErrTooSoon)
			require.Equal(t, stored.State, got.State,
				"a refusal still shows what is already known")
			require.Empty(t, prov.calls())
			require.Empty(t, store.written())
			requireBudgetLeft(t, budget, virusTotalPerMinute)
		})
	}
}

// Exactly a week later it is allowed. The floor is a week, not a week and a
// bit, and a control that says "you may ask again on the 30th" has to mean it.
func TestService_RefreshIsAllowedExactlyOnTheFloor(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderVirusTotal,
		fetchedAgo(clk, cleanVerdict(), reputation.ManualRefreshFloor))

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, 0)

	_, err := svc.Refresh(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Len(t, prov.calls(), 1)
}

// A detection is not re-checked by hand either. There is nothing to learn from
// re-confirming malware, and the allowance spent on it is real.
func TestService_RefreshIsRefusedOnADetection(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, detectedVerdict(), 400*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, 0)

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.ErrorIs(t, err, reputation.ErrDetectionIsFinal)
	require.Equal(t, reputation.Detected, got.State)
	require.Empty(t, prov.calls(), "a year-old detection is still a detection")
	require.Empty(t, store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// There is nothing to refresh on a file nobody has looked up. The ordinary
// lazy lookup covers that case, and a refresh that quietly became a first
// lookup would be a second way to spend the allowance with none of the rules.
func TestService_RefreshOnAFileWithNoVerdictIsNotALookup(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.ErrorIs(t, err, reputation.ErrNotCached)
	require.Equal(t, reputation.Unavailable, got.State)
	require.Empty(t, prov.calls())
	require.Empty(t, store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// The daily budget still applies. A button is not a reason to risk getting an
// operator's API key banned.
func TestService_RefreshStillObeysTheBudget(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, cleanVerdict(), 30*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, budget := newExpiringService(t, clk, prov, store, 0)

	for i := 0; i < virusTotalPerMinute; i++ {
		require.NoError(t, budget.Spend(reputation.ProviderVirusTotal))
	}

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.ErrorIs(t, err, reputation.ErrRateLimited)
	require.Equal(t, stored, got, "the refusal does not erase what is known")
	require.Empty(t, prov.calls())
	require.Empty(t, store.written())
}

// A re-check that comes back with the same answer is still written.
//
// Otherwise the row keeps its old fetched_at, the control re-arms the instant
// it is used, and the next reader spends another lookup learning the same
// nothing. The write is what moves the clock on both rules.
func TestService_ARefreshThatChangesNothingIsStillWritten(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	same := cleanVerdict()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, fetchedAgo(clk, same, 30*expiryDay))

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: same}
	svc, _ := newExpiringService(t, clk, prov, store, 0)

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, same.State, got.State)
	require.Equal(t,
		[]putRecord{{sha256: eicarSHA, provider: reputation.ProviderVirusTotal, rep: same}},
		store.written(), "an unchanged verdict must still be re-stamped")
}

// A re-check that does not complete does not erase the verdict it was
// re-checking, and does not cache the non-answer either.
func TestService_ARefreshThatFailsKeepsTheStoredVerdict(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	stored := fetchedAgo(clk, cleanVerdict(), 30*expiryDay)
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{
		name: reputation.ProviderVirusTotal,
		rep:  reputation.Reputation{State: reputation.Unavailable},
	}
	svc, _ := newExpiringService(t, clk, prov, store, 0)

	got, err := svc.Refresh(context.Background(), eicarSHA)
	require.Error(t, err)
	require.Equal(t, stored, got, "a failed re-check must not turn a verdict into no answer")
	require.Empty(t, store.written(), "unavailable is never cached, by either path")
}

// An unknown fetch time does not block a person who asked.
//
// It cannot happen against the database — attachment_reputation.fetched_at is
// NOT NULL — so this is about any other Store: the floor exists to stop a loop
// spending the allowance, the budget behind it still applies, and refusing a
// human being on a timestamp nobody recorded would make the control dead with
// no way to tell why.
func TestService_RefreshWithNoFetchTimeIsAllowed(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, cleanVerdict()) // no FetchedAt

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, 0)

	_, err := svc.Refresh(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Len(t, prov.calls(), 1)
}
