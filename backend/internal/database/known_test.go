package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The sixth state and the third provider, at the column that refuses
// everything else (#168).
//
// attachment_reputation.state and .provider are both CHECKed, which is what
// keeps the column and internal/reputation's constants agreeing on a spelling.
// The cost of that is that adding either without amending migration 000024
// produces a feature that works in every unit test and writes nothing on a
// real instance — the verdict is fetched, the insert fails, and the next
// render fetches it again.

// known is storable, spelled exactly as the Go constant spells it.
func TestAttachmentReputation_AcceptsTheKnownState(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	got, err := q.UpsertAttachmentReputation(context.Background(), dbgen.UpsertAttachmentReputationParams{
		Sha256:   repHashA,
		Provider: reputation.ProviderPolySwarm,
		State:    string(reputation.Known),
	})
	require.NoError(t, err)
	require.Equal(t, "known", got.State,
		"the column and the Go constant have to agree, or a cached verdict is unreachable")
}

// polyswarm is storable as a provider, for the same reason. A verdict written
// under a provider the CHECK refuses is never written at all; one written
// under a misspelled provider is written and never read back.
func TestAttachmentReputation_AcceptsPolySwarmAsAProvider(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	got, err := q.UpsertAttachmentReputation(context.Background(), dbgen.UpsertAttachmentReputationParams{
		Sha256:   repHashA,
		Provider: reputation.ProviderPolySwarm,
		State:    string(reputation.Clean),
	})
	require.NoError(t, err)
	require.Equal(t, "polyswarm", got.Provider)
}

// The provider CHECK still refuses everything else. A constraint that accepted
// the new value by being dropped would pass the test above.
func TestAttachmentReputation_StillRefusesAnUnknownProvider(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	for _, provider := range []string{"", "poly-swarm", "PolySwarm", "polyswarm "} {
		t.Run(provider, func(t *testing.T) {
			// A fresh transaction per case: a failed statement aborts the
			// surrounding one in Postgres.
			q, rollback := testutil.TxQueries(t, db)
			defer rollback()

			_, err := q.UpsertAttachmentReputation(context.Background(), dbgen.UpsertAttachmentReputationParams{
				Sha256:   repHashA,
				Provider: provider,
				State:    string(reputation.Clean),
			})
			require.Error(t, err, "provider %q must not be storable", provider)
		})
	}
}

// The feeds that carry a "known" file survive the round trip.
//
// They are the evidence AND the weight, and without them the verdict is a
// claim from nowhere: an Authenticode signature assertion and an NSRL
// catalogue entry are different things — NSRL catalogues hacking tools — and
// the staff member deciding whether to trust a binary is the one who needs to
// tell them apart.
func TestReputationStore_RoundTripsTheKnownFeeds(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	want := reputation.Reputation{
		State:      reputation.Known,
		KnownFeeds: []string{"microsoft_windows", "nsrl"},
	}
	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderPolySwarm, want))

	got, err := s.Get(ctx, repHashA, reputation.ProviderPolySwarm)
	require.NoError(t, err)
	require.Equal(t, reputation.Known, got.State)
	require.Equal(t, []string{"microsoft_windows", "nsrl"}, got.KnownFeeds)
}

// Every other verdict carries no feeds, and reads back carrying none.
//
// An empty list is the honest rendering of "nobody has this file catalogued".
// A leftover one would attach the only positive claim this system can make to
// a file nothing said anything about, which is the exact inversion of the rule
// the rest of this feature follows.
func TestReputationStore_AVerdictWithNoFeedsReadsBackWithNone(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repHashB, reputation.ProviderPolySwarm,
		reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 12}))

	got, err := s.Get(ctx, repHashB, reputation.ProviderPolySwarm)
	require.NoError(t, err)
	require.Empty(t, got.KnownFeeds)
}
