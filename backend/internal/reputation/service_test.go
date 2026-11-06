package reputation_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// Fakes.
//
// No database here. The cache's SQL behaviour — the composite key, the
// upsert, the CHECK constraints — has integration tests of its own in
// internal/database/attachment_reputation_test.go. What is under test in this
// file is the orchestration: which of the three collaborators gets called,
// with what, and in what order. The assertions that matter are about calls
// that did NOT happen, and those need a fake that records rather than a
// database that forgets.
// ---------------------------------------------------------------------------

type readRecord struct {
	sha256   string
	provider string
}

type putRecord struct {
	sha256   string
	provider string
	rep      reputation.Reputation
}

// fakeStore is an in-memory verdict cache that records every read and every
// attempted write.
type fakeStore struct {
	mu   sync.Mutex
	rows map[readRecord]reputation.Reputation

	getErr error // when set, every Get fails with it
	putErr error // when set, every Put fails with it

	reads  []readRecord
	writes []putRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[readRecord]reputation.Reputation{}}
}

// seed puts a verdict in the cache without recording it as a write, so that
// "the service wrote nothing" assertions stay readable.
func (s *fakeStore) seed(sha256, provider string, rep reputation.Reputation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[readRecord{sha256, provider}] = rep
}

func (s *fakeStore) Get(_ context.Context, sha256, provider string) (reputation.Reputation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, readRecord{sha256, provider})
	if s.getErr != nil {
		return reputation.Reputation{}, s.getErr
	}
	rep, ok := s.rows[readRecord{sha256, provider}]
	if !ok {
		return reputation.Reputation{}, reputation.ErrNotCached
	}
	return rep, nil
}

func (s *fakeStore) Put(_ context.Context, sha256, provider string, rep reputation.Reputation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Recorded before the failure branch: an attempted write is the thing
	// several tests below assert did not happen, and a write that failed was
	// still attempted.
	s.writes = append(s.writes, putRecord{sha256, provider, rep})
	if s.putErr != nil {
		return s.putErr
	}
	s.rows[readRecord{sha256, provider}] = rep
	return nil
}

func (s *fakeStore) readsSeen() []readRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]readRecord(nil), s.reads...)
}

func (s *fakeStore) written() []putRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]putRecord(nil), s.writes...)
}

func (s *fakeStore) row(t *testing.T, sha256, provider string) reputation.Reputation {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	rep, ok := s.rows[readRecord{sha256, provider}]
	require.Truef(t, ok, "no cached verdict for %s under %s", sha256, provider)
	return rep
}

// fakeProvider answers every lookup the same way and records what it was
// asked.
type fakeProvider struct {
	name string
	rep  reputation.Reputation
	err  error

	mu      sync.Mutex
	lookups []string
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Lookup(_ context.Context, sha256 string) (reputation.Reputation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lookups = append(p.lookups, sha256)
	return p.rep, p.err
}

func (p *fakeProvider) LinkURL(sha256 string) string {
	return "https://example.test/file/" + sha256
}

func (p *fakeProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.lookups...)
}

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

// detectedVerdict is a complete answer: every field populated, so that a test
// comparing whole values catches a verdict that was rebuilt on the way out
// with its counts or its analysis date dropped. The numbers match the
// VirusTotal fixture in virustotal_test.go.
func detectedVerdict() reputation.Reputation {
	at := time.Date(2025, 9, 22, 18, 20, 0, 0, time.UTC)
	return reputation.Reputation{
		State:      reputation.Detected,
		Detected:   62,
		Total:      81,
		ThreatName: "Trojan.GenericKD.12345",
		AnalysedAt: &at,
	}
}

func cleanVerdict() reputation.Reputation {
	at := time.Date(2025, 9, 22, 18, 20, 0, 0, time.UTC)
	return reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 78, AnalysedAt: &at}
}

// newService wires a service on a frozen clock.
//
// The clock is frozen deliberately: VirusTotal's four-a-minute bucket then
// becomes a precise meter for how much budget a call spent, which is the only
// way to observe budget consumption from outside the package.
func newService(t *testing.T, prov *fakeProvider, store *fakeStore) (*reputation.Service, *reputation.Budget) {
	t.Helper()
	b := newClock(t, "2025-09-22T10:00:00Z").budget()
	return reputation.NewService(prov, store, b), b
}

// requireBudgetLeft asserts exactly n of VirusTotal's four lookups a minute
// remain unspent. Used as the meter described above.
func requireBudgetLeft(t *testing.T, b *reputation.Budget, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoErrorf(t, b.Spend(reputation.ProviderVirusTotal),
			"expected %d of the minute's lookups left, but only %d were", n, i)
	}
	require.ErrorIs(t, b.Spend(reputation.ProviderVirusTotal), reputation.ErrRateLimited,
		"expected only %d of the minute's lookups left, but there were more", n)
}

// ---------------------------------------------------------------------------
// The cache.
// ---------------------------------------------------------------------------

// A stored verdict is the answer. No provider call, no budget spent.
//
// This is the whole economic argument for the table: a file uploaded to five
// tickets costs one lookup, and a ticket page reopened all afternoon costs
// none. An implementation that reads the cache and looks up anyway passes a
// test that only checks the returned value — the provider here is primed with
// a DIFFERENT verdict so that it cannot.
func TestService_ACacheHitCostsNoLookupAndNoBudget(t *testing.T) {
	store := newFakeStore()
	stored := detectedVerdict()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, budget := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, stored, got, "the stored verdict is the answer, whole and unrebuilt")

	require.Empty(t, prov.calls(), "a stored verdict is never re-fetched")
	require.Empty(t, store.written(), "a hit must not rewrite the row it just read")
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// A miss spends one lookup, asks the provider, returns what it said, and keeps
// it.
//
// All four, and each is load-bearing: a miss that does not store re-looks-up
// on every render and burns the day's 500 on one file; one that stores
// something other than what the provider said caches a lie.
func TestService_ACacheMissLooksUpSpendsOnceAndStores(t *testing.T) {
	store := newFakeStore()
	verdict := detectedVerdict()
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: verdict}
	svc, budget := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, verdict, got)

	require.Equal(t, []string{eicarSHA}, prov.calls(), "exactly one lookup, for the hash we asked about")
	require.Equal(t,
		[]putRecord{{sha256: eicarSHA, provider: reputation.ProviderVirusTotal, rep: verdict}},
		store.written(),
		"the verdict is cached under the hash and the provider that produced it, unaltered")

	// One lookup's worth of budget and no more: three of the minute's four
	// are left.
	requireBudgetLeft(t, budget, virusTotalPerMinute-1)
}

// The cache is keyed on (sha256, provider), not on the hash alone.
//
// An operator who switches provider must not be shown the other service's
// answer attributed to the one they chose. The two services disagree about
// files routinely, and "VirusTotal says clean" rendered as MetaDefender's
// verdict is a false attribution as well as a false verdict.
func TestService_TheCacheIsKeyedOnProviderAsWellAsHash(t *testing.T) {
	store := newFakeStore()
	vtVerdict := detectedVerdict()
	store.seed(eicarSHA, reputation.ProviderVirusTotal, vtVerdict)

	mdVerdict := reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 38}
	prov := &fakeProvider{name: reputation.ProviderMetaDefender, rep: mdVerdict}
	svc, _ := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)
	require.Equal(t, mdVerdict, got, "the configured provider's answer, not the other one's")

	require.Equal(t, []readRecord{{eicarSHA, reputation.ProviderMetaDefender}}, store.readsSeen(),
		"the cache was asked for the configured provider's row")
	require.Equal(t, []string{eicarSHA}, prov.calls(), "a row under the other provider is still a miss")
	require.Equal(t,
		[]putRecord{{sha256: eicarSHA, provider: reputation.ProviderMetaDefender, rep: mdVerdict}},
		store.written())

	require.Equal(t, vtVerdict, store.row(t, eicarSHA, reputation.ProviderVirusTotal),
		"the other provider's row is not overwritten; switching back must find it intact")
}

// ---------------------------------------------------------------------------
// What must never be cached.
// ---------------------------------------------------------------------------

// Unavailable is never written.
//
// This is the most consequential rule in the package. A verdict is kept
// forever and never re-fetched, so caching a transient failure freezes it: a
// file whose scan was still running the first time anyone looked would read
// "we have no answer" for the life of the instance, and a minute of rate
// limiting would permanently poison every hash looked up during it.
//
// The assertion is that the store was not WRITTEN, not merely that something
// sensible came back — a service that writes the row and returns Unavailable
// passes the weaker check and still freezes the instance. The second call
// proves the same thing from the other side: the next render tries again.
func TestService_UnavailableIsNeverStored(t *testing.T) {
	cases := []struct {
		name string
		rep  reputation.Reputation
		err  error
	}{
		{
			name: "the provider could not be reached",
			rep:  reputation.Reputation{State: reputation.Unavailable},
			err:  errors.New("requesting: connection refused"),
		},
		{
			// MetaDefender returns hashes whose scan is still running.
			name: "the scan is still in progress",
			rep:  reputation.Reputation{State: reputation.Unavailable},
			err:  nil,
		},
		{
			name: "the provider throttled us",
			rep:  reputation.Reputation{State: reputation.Unavailable},
			err:  fmt.Errorf("virustotal: TooManyRequestsError: %w", reputation.ErrRateLimited),
		},
		{
			// A provider that fills in counts alongside Unavailable. Passing
			// them through renders "3 of 70 engines" next to "no answer",
			// which reads as a verdict.
			name: "unavailable carrying numbers it has no right to",
			rep:  reputation.Reputation{State: reputation.Unavailable, Detected: 3, Total: 70},
			err:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: tc.rep, err: tc.err}
			svc, _ := newService(t, prov, store)

			got, _ := svc.GetOrLookup(context.Background(), eicarSHA)
			require.Equal(t, reputation.Unavailable, got.State)
			require.Zero(t, got.Detected, "a lookup with no answer has no numbers")
			require.Zero(t, got.Total)
			require.Nil(t, got.AnalysedAt)

			require.Empty(t, store.written(),
				"an unavailable verdict was written to the cache, where it would stay forever")

			// And the next render tries again rather than reading a frozen
			// failure back.
			_, _ = svc.GetOrLookup(context.Background(), eicarSHA)
			require.Len(t, prov.calls(), 2, "the failure was cached: the second render did not look up")
		})
	}
}

// A provider error never becomes a verdict.
//
// The provider here returns a Clean reputation AND an error, which is what a
// half-written parser does: it fills in a value on a path it also failed on.
// Returning the pair unchanged — the obvious implementation — renders "0 of 70
// engines found anything" for a file nobody successfully looked up. That is
// the exact defect this project has fixed twice in the ClamAV scanner
// (c47db36, abbbabe).
func TestService_AProviderErrorNeverBecomesAVerdict(t *testing.T) {
	store := newFakeStore()
	prov := &fakeProvider{
		name: reputation.ProviderVirusTotal,
		rep:  reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 70},
		err:  errors.New("decoding response: unexpected end of JSON input"),
	}
	svc, _ := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.Error(t, err, "the operator still needs the reason")
	require.Equal(t, reputation.Unavailable, got.State, "a failed lookup is not a clean file")
	require.Zero(t, got.Total, "and it has no denominator either")
	require.Empty(t, store.written(), "a failed lookup must not be cached as a verdict")
}

// A cache read that fails is not a verdict either.
//
// A dead or unreachable database returns neither a row nor ErrNotCached. The
// value the caller gets must be Unavailable and not the zero Reputation the
// store handed back — State "" has no branch in any renderer and falls through
// to the arm that draws nothing wrong.
//
// The provider is deliberately not called: a store that cannot be read
// probably cannot be written either, so looking up would spend budget on a
// verdict with nowhere to go, once per render, for the length of the outage.
func TestService_AFailedCacheReadIsNotAVerdict(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("read tcp 127.0.0.1:5432: connection reset by peer")

	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	svc, budget := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.Error(t, err)
	require.Equal(t, reputation.Unavailable, got.State)

	require.Empty(t, prov.calls(), "a cache that cannot be read is not a reason to spend a lookup")
	require.Empty(t, store.written())
	requireBudgetLeft(t, budget, virusTotalPerMinute)
}

// A cache write that fails does not make the verdict less true.
//
// The direction matters. Dropping the verdict because the row would not save
// turns "this file is malware" into "not checked yet" on a database hiccup,
// which is the one substitution this feature must never make. The operator
// gets the error; the reader gets the verdict.
func TestService_AFailedCacheWriteStillReturnsTheVerdict(t *testing.T) {
	store := newFakeStore()
	store.putErr = errors.New("pq: cannot execute INSERT in a read-only transaction")

	verdict := detectedVerdict()
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: verdict}
	svc, _ := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.Error(t, err, "the operator needs to know the cache did not take")
	require.Equal(t, verdict, got, "a verdict that could not be cached is still a verdict")
}

// ---------------------------------------------------------------------------
// The budget.
// ---------------------------------------------------------------------------

// An exhausted budget renders "not checked yet". It does not call out anyway,
// and it does not cache the non-answer.
//
// Both refusals are covered because they arrive by different paths and the
// caller has to be able to tell them apart afterwards: the reason is wrapped,
// not swallowed, so a handler can log "throttled, clears in a minute" rather
// than "quota gone until tomorrow".
func TestService_AnExhaustedBudgetDoesNotCallTheProvider(t *testing.T) {
	t.Run("the minute bucket is empty", func(t *testing.T) {
		store := newFakeStore()
		prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: detectedVerdict()}
		svc, budget := newService(t, prov, store)

		for i := 0; i < virusTotalPerMinute; i++ {
			require.NoError(t, budget.Spend(reputation.ProviderVirusTotal))
		}

		got, err := svc.GetOrLookup(context.Background(), eicarSHA)
		require.Equal(t, reputation.Unavailable, got.State, "no budget is no answer, not a clean file")
		require.ErrorIs(t, err, reputation.ErrRateLimited, "the reason must survive to the handler")
		require.Empty(t, prov.calls(), "the whole point of the budget is that this call is not made")
		require.Empty(t, store.written())
	})

	t.Run("the day is spent", func(t *testing.T) {
		store := newFakeStore()
		prov := &fakeProvider{name: reputation.ProviderMetaDefender, rep: detectedVerdict()}
		svc, budget := newService(t, prov, store)

		for i := 0; i < metaDefenderDaily; i++ {
			require.NoError(t, budget.Spend(reputation.ProviderMetaDefender))
		}

		got, err := svc.GetOrLookup(context.Background(), eicarSHA)
		require.Equal(t, reputation.Unavailable, got.State)
		require.ErrorIs(t, err, reputation.ErrQuotaExceeded)
		require.Empty(t, prov.calls())
		require.Empty(t, store.written())
	})
}

// ---------------------------------------------------------------------------
// A state nothing can render.
//
// FINDING, and the one test here written as a claim about what the code should
// do rather than as a pin on what it does. It fails today. See the report.
//
// Migration 000024 CHECKs attachment_reputation.state against the five, and
// says why in the migration itself: a zero-valued Reputation has State "",
// nothing downstream has a branch for the empty string, and it falls through
// to whatever the no-problem-here arm renders. The database is the last place
// the value can be wrong — but it is not the only place it is read. On a
// failed write the Service returns the verdict to the caller anyway (correctly
// — see AFailedCacheWriteStillReturnsTheVerdict), so a state the column
// refused still reaches the page.
//
// The Service already refuses to write one state on its own judgement. Judging
// "this is not a state at all" by the same rule costs one comparison and
// closes the gap between the CHECK and the screen.
// ---------------------------------------------------------------------------

func TestService_AStateTheDatabaseWouldRefuseIsNeitherStoredNorReturned(t *testing.T) {
	renderable := map[reputation.State]bool{
		reputation.Unseen:      true,
		reputation.Unscanned:   true,
		reputation.Clean:       true,
		reputation.Detected:    true,
		reputation.Unavailable: true,
	}

	cases := []struct {
		name string
		rep  reputation.Reputation
	}{
		// The forgotten return path: a provider falls off the end of a switch
		// and hands back its zero value with no error.
		{"the zero value", reputation.Reputation{}},
		// A spelling the CHECK constraint refuses and no renderer knows.
		{"a state that is not one of the five", reputation.Reputation{State: reputation.State("infected")}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: tc.rep}
			svc, _ := newService(t, prov, store)

			got, _ := svc.GetOrLookup(context.Background(), eicarSHA)

			// Not require: the two claims below fail independently and both
			// are worth seeing in one run.
			if !renderable[got.State] {
				t.Errorf("State %q reached the caller; nothing downstream has a branch for it, "+
					"so it renders as the no-problem-here arm", got.State)
			}
			require.Empty(t, store.written(),
				"a state attachment_reputation.state would refuse was handed to the cache")
		})
	}
}
