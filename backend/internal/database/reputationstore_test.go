package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/reputationstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// reputationstore is the adapter between internal/reputation's Store interface
// and the generated queries. The table's own behaviour — the key, the CHECKs,
// the nullability — is pinned in attachment_reputation_test.go; what is pinned
// here is the translation, which is where a miss can be turned into a verdict
// and a NULL into a zero.

// A miss is ErrNotCached and nothing else.
//
// This is the whole reason the adapter exists. sql.ErrNoRows arriving at the
// service would fall through the `errors.Is(err, ErrNotCached)` arm into
// "reading cached verdict failed", and the lookup that should have happened
// never would: every render of every quarantined attachment would report
// Unavailable forever, with the provider never once called.
func TestReputationStore_AMissIsErrNotCached(t *testing.T) {
	s, _ := newReputationStore(t)

	rep, err := s.Get(context.Background(), repHashA, reputation.ProviderVirusTotal)
	require.ErrorIs(t, err, reputation.ErrNotCached)
	require.NotEqual(t, reputation.Clean, rep.State,
		"a miss must never come back as a verdict")
}

// A verdict survives the round trip with every field intact.
func TestReputationStore_RoundTripsADetection(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	analysed := time.Date(2025, 9, 22, 18, 20, 0, 0, time.UTC)
	want := reputation.Reputation{
		State:      reputation.Detected,
		Detected:   62,
		Total:      81,
		ThreatName: "Trojan.GenericKD.12345",
		AnalysedAt: &analysed,
	}
	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal, want))

	got, err := s.Get(ctx, repHashA, reputation.ProviderVirusTotal)
	require.NoError(t, err)
	require.Equal(t, want.State, got.State)
	require.Equal(t, 62, got.Detected)
	require.Equal(t, 81, got.Total)
	require.Equal(t, want.ThreatName, got.ThreatName)
	require.NotNil(t, got.AnalysedAt)
	require.True(t, analysed.Equal(*got.AnalysedAt), "got %v", got.AnalysedAt)
}

// A verdict is a statement by one provider. Reading the other one's answer
// under the configured name is the silent failure the composite key exists to
// prevent, and the adapter has to pass both halves of it through.
func TestReputationStore_DoesNotReadOneProvidersAnswerAsAnothers(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Detected, Detected: 62, Total: 81}))

	_, err := s.Get(ctx, repHashA, reputation.ProviderMetaDefender)
	require.ErrorIs(t, err, reputation.ErrNotCached)
}

// No numbers means NULL in the column and not zero.
//
// "unseen" with detected = 0 and total = 0 renders as "no engine found
// anything", which is the false reassurance the whole feature exists to avoid.
// Asserted against the raw row rather than through Get, because Get hands back
// Go ints either way and cannot tell the two apart.
func TestReputationStore_WritesNoNumbersAsNull(t *testing.T) {
	s, q := newReputationStore(t)
	ctx := context.Background()

	for _, state := range []reputation.State{reputation.Unseen, reputation.Unscanned} {
		t.Run(string(state), func(t *testing.T) {
			require.NoError(t, s.Put(ctx, repHashB, reputation.ProviderVirusTotal,
				reputation.Reputation{State: state}))

			row, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
				Sha256: repHashB, Provider: reputation.ProviderVirusTotal,
			})
			require.NoError(t, err)
			require.Equal(t, string(state), row.State)
			require.False(t, row.Detected.Valid, "detected must be NULL, not 0")
			require.False(t, row.Total.Valid, "total must be NULL, not 0")
			require.False(t, row.ThreatName.Valid, "an absent threat name is NULL, not \"\"")
			require.False(t, row.AnalysedAt.Valid, "nobody analysed it, so there is no date")
		})
	}
}

// A clean verdict keeps its counts. 0 of 70 is a fact a person reads; it is
// only the states with no analysis behind them that must be NULL.
func TestReputationStore_KeepsTheCountsOnACleanVerdict(t *testing.T) {
	s, q := newReputationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repHashB, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Clean, Detected: 0, Total: 70}))

	row, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashB, Provider: reputation.ProviderVirusTotal,
	})
	require.NoError(t, err)
	require.True(t, row.Detected.Valid)
	require.Equal(t, int32(0), row.Detected.Int32)
	require.True(t, row.Total.Valid)
	require.Equal(t, int32(70), row.Total.Int32)
}

// Two staff members opening the same ticket at once both write. The second
// write must replace the first rather than fail on the primary key, which
// would 500 an ordinary page render.
func TestReputationStore_PutIsAnUpsert(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Clean, Total: 70}))
	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Detected, Detected: 3, Total: 70}))

	got, err := s.Get(ctx, repHashA, reputation.ProviderVirusTotal)
	require.NoError(t, err)
	require.Equal(t, reputation.Detected, got.State)
	require.Equal(t, 3, got.Detected)
}

// A state outside the five is refused, and the refusal reaches the caller as
// an error rather than as a silently missing row.
func TestReputationStore_RefusesAnUnknownState(t *testing.T) {
	s, _ := newReputationStore(t)

	err := s.Put(context.Background(), repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{})
	require.Error(t, err, "a zero-valued Reputation has State \"\" and must not be stored")
}

func newReputationStore(t *testing.T) (*reputationstore.Store, *dbgen.Queries) {
	t.Helper()
	q := repQueries(t)
	return reputationstore.New(q), q
}
