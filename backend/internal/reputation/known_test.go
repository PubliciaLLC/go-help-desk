package reputation_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// known, the sixth state.
//
// It is the one verdict in this feature where reassurance is the correct
// rendering, and it is an exception because a named feed made a positive
// claim. clean, unseen and unscanned have nothing of the sort behind them:
// clean means engines ran and found nothing, unseen means nobody has ever
// submitted the file, unscanned means the provider holds it and has no
// opinion. known means "a named feed has this exact hash on file, and here is
// which".
//
// "known" and not "known good": how much the claim is worth depends on the
// feed, which is why the feed names travel with it. An Authenticode signature
// assertion says the file is signed and trusted; an NSRL catalogue entry says
// only that it appeared in a software distribution, and NSRL catalogues
// hacking tools.
// ---------------------------------------------------------------------------

// knownVerdict is a complete answer of the new shape: a state, the feeds that
// carry the file, and no engine counts, because a file answered out of a
// catalogue is never scanned.
func knownVerdict() reputation.Reputation {
	at := time.Date(2026, 8, 14, 9, 12, 33, 0, time.UTC)
	return reputation.Reputation{
		State:      reputation.Known,
		KnownFeeds: []string{"microsoft_windows", "nsrl"},
		AnalysedAt: &at,
	}
}

// A "known" verdict is cached like any other completed lookup.
//
// The failure this catches is the one the Service is built to catch for
// Unavailable, applied by mistake to the opposite case: a state the Service
// does not recognise is refused at the write AND at the return, so a sixth
// state added to the provider and forgotten in known() never reaches a page
// and never reaches the table, and the only symptom is a lookup spent on every
// single render.
func TestKnown_IsStoredAndReturned(t *testing.T) {
	store := newFakeStore()
	prov := &fakeProvider{name: reputation.ProviderPolySwarm, rep: knownVerdict()}
	svc, _ := newService(t, prov, store)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Known, got.State,
		"known is a declared state; a Service that does not know it refuses it")
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, got.KnownFeeds)

	written := store.written()
	require.Len(t, written, 1, "a completed verdict belongs in the cache")
	require.Equal(t, reputation.Known, written[0].rep.State)
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, written[0].rep.KnownFeeds,
		"the feeds are the evidence and the weight; a verdict cached without them is a claim from nowhere")
}

// A "known" verdict never expires, like a detection and unlike everything
// else.
//
// clean, unseen and unscanned all decay — new signatures catch old malware,
// and the sample nobody had submitted when we asked is the one submitted a
// week later. This one does not: a hash does not fall out of a vendor
// catalogue, and spending an hourly allowance of sixty re-confirming one is
// the lookup guaranteed to tell nobody anything.
//
// The provider is primed with a DIFFERENT verdict, so an implementation that
// re-checks cannot pass by returning the same answer twice.
func TestKnown_NeverExpires(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	// A year old, against a fortnight's interval.
	store.seed(eicarSHA, reputation.ProviderPolySwarm, fetchedAgo(clk, knownVerdict(), 365*expiryDay))

	prov := &fakeProvider{name: reputation.ProviderPolySwarm, rep: detectedVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Known, got.State)
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, got.KnownFeeds)
	require.Empty(t, prov.calls(),
		"a year-old known verdict was re-checked; a hash does not fall out of a catalogue")
	require.Empty(t, store.written())
}

// The companion, so that "never expires" is not satisfied by a cache that
// never expires anything: a clean verdict of exactly the same age IS
// re-checked.
func TestKnown_ACleanVerdictOfTheSameAgeIsStillRechecked(t *testing.T) {
	clk := newClock(t, "2026-09-23T10:00:00Z")
	store := newFakeStore()
	store.seed(eicarSHA, reputation.ProviderPolySwarm, fetchedAgo(clk, cleanVerdict(), 365*expiryDay))

	prov := &fakeProvider{name: reputation.ProviderPolySwarm, rep: detectedVerdict()}
	svc, _ := newExpiringService(t, clk, prov, store, expiryBiweekly)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)
	require.NoError(t, err)

	require.Equal(t, reputation.Detected, got.State)
	require.Len(t, prov.calls(), 1, "a year-old clean verdict is exactly what expiry exists for")
}
