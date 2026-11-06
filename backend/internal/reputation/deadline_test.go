package reputation_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// Service.Deadline bounds a whole request's worth of lookups, not one call.
//
// The per-client timeout in client.go bounds one HTTP call, which is the wrong
// unit: a page render asks every enabled provider about every quarantined
// attachment in series, so N providers times M files times that timeout is
// what a hung endpoint actually costs. The caller hands every service in one
// request the same instant; these pin what happens once it is behind us.

// Past the deadline, nobody is asked and nothing is spent.
//
// The budget is the operator's allowance at a third party and it counts
// requests to one. No request goes out here, so taking one off the count would
// charge them for a call nobody made — and on a ticket with a dozen
// attachments it would empty a 500-a-day allowance against a provider that has
// not been contacted once.
func TestService_APassedDeadlineAsksNobodyAndSpendsNothing(t *testing.T) {
	prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
	store := newFakeStore()
	svc, budget := newService(t, prov, store)
	svc.Deadline = time.Now().Add(-time.Second)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)

	require.ErrorIs(t, err, reputation.ErrDeadlinePassed)
	require.Equal(t, reputation.Unavailable, got.State,
		"a lookup that never happened is 'we do not know', never clean")
	require.Empty(t, prov.calls(), "the deadline had passed, so nobody was asked")
	require.Empty(t, store.written(), "unavailable is never cached")
	requireBudgetLeft(t, budget, 4)
}

// But the cache is still served, and that is the half it would be easy to
// break.
//
// The deadline covers the outbound call and nothing else. Bound the whole
// operation instead and one hung provider erases every verdict already on file
// for the rest of the request: the page would show "unavailable" for answers
// it holds and could render without touching the network, which is the exact
// opposite of what a cache is for.
func TestService_APassedDeadlineStillServesTheCache(t *testing.T) {
	t.Run("a fresh verdict is returned untouched", func(t *testing.T) {
		prov := &fakeProvider{name: reputation.ProviderVirusTotal}
		store := newFakeStore()
		want := detectedVerdict()
		want.FetchedAt = time.Now().Add(-time.Hour)
		store.seed(eicarSHA, reputation.ProviderVirusTotal, want)

		svc, _ := newService(t, prov, store)
		svc.Deadline = time.Now().Add(-time.Second)

		got, err := svc.GetOrLookup(context.Background(), eicarSHA)

		require.NoError(t, err, "nothing failed: the answer was already on file")
		require.Equal(t, want, got)
		require.Empty(t, prov.calls())
	})

	t.Run("a stale verdict survives the re-check it could not make", func(t *testing.T) {
		prov := &fakeProvider{name: reputation.ProviderVirusTotal, rep: cleanVerdict()}
		store := newFakeStore()
		stored := cleanVerdict()
		stored.FetchedAt = time.Now().Add(-30 * 24 * time.Hour)
		store.seed(eicarSHA, reputation.ProviderVirusTotal, stored)

		svc, _ := newService(t, prov, store)
		svc.RefreshAfter = 14 * 24 * time.Hour
		svc.Deadline = time.Now().Add(-time.Second)

		got, err := svc.GetOrLookup(context.Background(), eicarSHA)

		require.ErrorIs(t, err, reputation.ErrDeadlinePassed)
		require.Equal(t, stored, got,
			"a month-old clean verdict with its date attached beats 'not checked yet'")
		require.Empty(t, prov.calls())
	})
}

// And a lookup already in flight when the deadline arrives is cut, rather than
// running on to the client's own fifteen seconds.
//
// This is the case that made the bug: a provider that accepts the connection
// and never answers, which is what an outbound firewall dropping packets looks
// like from here. A refused connection was never the problem — it is instant.
func TestService_ADeadlineCutsALookupInFlight(t *testing.T) {
	prov := &hangingProvider{name: reputation.ProviderVirusTotal}
	store := newFakeStore()
	svc := reputation.NewService(prov, store, reputation.NewBudget())
	svc.Deadline = time.Now().Add(150 * time.Millisecond)

	start := time.Now()
	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Equal(t, reputation.Unavailable, got.State)
	require.Less(t, elapsed, 5*time.Second,
		"the provider never answers; the deadline is what has to end this, and it took %v", elapsed)
	require.Empty(t, store.written(), "unavailable is never cached")
}

// hangingProvider answers only when its context is cancelled, which is what a
// dropped packet looks like to the client underneath a real provider.
type hangingProvider struct{ name string }

func (p *hangingProvider) Name() string { return p.name }

func (p *hangingProvider) Lookup(ctx context.Context, _ string) (reputation.Reputation, error) {
	<-ctx.Done()
	return reputation.Reputation{State: reputation.Unavailable}, ctx.Err()
}

func (p *hangingProvider) LinkURL(sha256 string) string {
	return "https://example.test/file/" + sha256
}
