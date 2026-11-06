package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The verdict cache behind internal/reputation: table attachment_reputation,
// migration 000024, primary key (sha256, provider).
//
// These are integration tests against a real Postgres because the behaviour
// being pinned is the key and the nullability, which live in the schema. A
// mock of the store would assert that the mock does what the test told it to.

const (
	repHashA = "275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f"
	repHashB = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func repQueries(t *testing.T) *dbgen.Queries {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, rollback := testutil.TxQueries(t, db)
	t.Cleanup(rollback)
	return q
}

// A verdict is a statement by one provider, not a property of the file.
//
// An operator who switches from VirusTotal to MetaDefender must never be shown
// the other service's answer attributed to the one they chose — and the
// failure mode is silent, because a cached row looks identical whichever
// service wrote it. Two rows per hash is what makes that impossible rather
// than merely unlikely.
func TestAttachmentReputation_AVerdictBelongsToOneProvider(t *testing.T) {
	q := repQueries(t)
	ctx := context.Background()

	analysed := time.Date(2025, 9, 22, 18, 20, 0, 0, time.UTC)
	_, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
		Sha256:     repHashA,
		Provider:   reputation.ProviderVirusTotal,
		State:      string(reputation.Detected),
		Detected:   sql.NullInt32{Int32: 62, Valid: true},
		Total:      sql.NullInt32{Int32: 81, Valid: true},
		ThreatName: sql.NullString{String: "Trojan.GenericKD.12345", Valid: true},
		AnalysedAt: sql.NullTime{Time: analysed, Valid: true},
	})
	require.NoError(t, err)

	// The provider that wrote it reads it back, whole.
	got, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderVirusTotal,
	})
	require.NoError(t, err)
	require.Equal(t, string(reputation.Detected), got.State)
	require.Equal(t, int32(62), got.Detected.Int32)
	require.Equal(t, int32(81), got.Total.Int32)
	require.Equal(t, "Trojan.GenericKD.12345", got.ThreatName.String)
	require.True(t, got.AnalysedAt.Valid)
	require.True(t, got.AnalysedAt.Time.UTC().Equal(analysed))
	require.False(t, got.FetchedAt.IsZero(), "we always know when we asked")

	// The other provider has said nothing about this hash, and a miss must
	// come back as a miss.
	_, err = q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderMetaDefender,
	})
	require.ErrorIs(t, err, sql.ErrNoRows,
		"MetaDefender has no verdict for this hash; VirusTotal's is not its answer")

	// Both providers can hold a verdict for the same hash, and they do not
	// overwrite each other.
	_, err = q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
		Sha256:   repHashA,
		Provider: reputation.ProviderMetaDefender,
		State:    string(reputation.Clean),
		Detected: sql.NullInt32{Int32: 0, Valid: true},
		Total:    sql.NullInt32{Int32: 37, Valid: true},
	})
	require.NoError(t, err)

	vt, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderVirusTotal,
	})
	require.NoError(t, err)
	require.Equal(t, string(reputation.Detected), vt.State,
		"MetaDefender's clean verdict must not have overwritten VirusTotal's detection")

	md, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderMetaDefender,
	})
	require.NoError(t, err)
	require.Equal(t, string(reputation.Clean), md.State)
	require.Equal(t, int32(37), md.Total.Int32)
}

// A hash nobody has looked up must read as a miss, not as a zero-valued row.
//
// This is the cache's version of the defect the whole feature exists to
// prevent: a caller that cannot tell "no verdict" from "a verdict of clean"
// renders the first as the second.
func TestAttachmentReputation_AMissIsAMiss(t *testing.T) {
	q := repQueries(t)
	_, err := q.GetAttachmentReputation(context.Background(), dbgen.GetAttachmentReputationParams{
		Sha256: repHashB, Provider: reputation.ProviderVirusTotal,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// Two staff opening the same ticket at once both trigger the lazy lookup. Without
// ON CONFLICT the second insert fails on the primary key and an ordinary page
// render errors — so the second write must succeed and must win.
func TestAttachmentReputation_UpsertReplacesAnEarlierVerdict(t *testing.T) {
	q := repQueries(t)
	ctx := context.Background()

	first, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
		Sha256:   repHashA,
		Provider: reputation.ProviderVirusTotal,
		State:    string(reputation.Unseen),
	})
	require.NoError(t, err)

	second, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
		Sha256:     repHashA,
		Provider:   reputation.ProviderVirusTotal,
		State:      string(reputation.Detected),
		Detected:   sql.NullInt32{Int32: 62, Valid: true},
		Total:      sql.NullInt32{Int32: 81, Valid: true},
		ThreatName: sql.NullString{String: "Trojan.GenericKD.12345", Valid: true},
	})
	require.NoError(t, err, "a concurrent second lookup must not error on the primary key")
	require.Equal(t, string(reputation.Detected), second.State)
	require.Equal(t, first.Sha256, second.Sha256)

	got, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderVirusTotal,
	})
	require.NoError(t, err)
	require.Equal(t, string(reputation.Detected), got.State, "one row per (hash, provider)")
	require.Equal(t, int32(62), got.Detected.Int32)
}

// For unseen, unscanned and unavailable the provider gave us no numbers, and
// the columns must stay NULL.
//
// 0 and 0 would render as "no engine found anything", which is exactly the
// false reassurance this feature exists to avoid. NULL means "not recorded",
// and a caller reading .Valid cannot mistake it for a count.
func TestAttachmentReputation_NoNumbersMeansNullNotZero(t *testing.T) {
	q := repQueries(t)
	ctx := context.Background()

	for _, state := range []reputation.State{reputation.Unseen, reputation.Unscanned, reputation.Unavailable} {
		t.Run(string(state), func(t *testing.T) {
			_, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
				Sha256:   repHashA,
				Provider: reputation.ProviderVirusTotal,
				State:    string(state),
				// Detected, Total, ThreatName and AnalysedAt all left invalid:
				// that is what "the provider told us nothing" looks like.
			})
			require.NoError(t, err)

			got, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
				Sha256: repHashA, Provider: reputation.ProviderVirusTotal,
			})
			require.NoError(t, err)
			require.Equal(t, string(state), got.State)
			require.False(t, got.Detected.Valid, "0 detections is not the same as no data")
			require.False(t, got.Total.Valid, "0 of 0 must be unrepresentable here")
			require.False(t, got.ThreatName.Valid)
			require.False(t, got.AnalysedAt.Valid,
				"the provider did not analyse it, so there is no analysis date")
		})
	}
}

// Every row records one of the five states, and the database is where that is
// enforced.
//
// The value this stops is the empty string. A zero-valued reputation.Reputation
// — the thing every "not implemented" stub and every forgotten error path
// returns — has State "", and nothing downstream has a branch for it, so it
// falls through to whatever the no-problem-here arm renders. That row must not
// be storable in the first place.
//
// Currently red: migration 000024 declares state TEXT NOT NULL with no CHECK.
// See the open question in the report — a CHECK constraint is the smallest fix
// and puts the rule next to the column it governs.
func TestAttachmentReputation_RefusesAStateThatIsNotOneOfTheFive(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	bad := []struct{ name, state string }{
		{"empty string from a zero value", ""},
		{"a state nobody declared", "ok"},
		{"the antivirus package's spelling", "infected"},
		{"right word, wrong case", "Clean"},
		{"whitespace", " clean"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh transaction per case: a failed statement aborts the
			// surrounding transaction in Postgres, so they cannot share one.
			q, rollback := testutil.TxQueries(t, db)
			defer rollback()

			_, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
				Sha256:   repHashA,
				Provider: reputation.ProviderVirusTotal,
				State:    tc.state,
			})
			require.Error(t, err, "state %q must not be storable", tc.state)
			require.False(t, errors.Is(err, sql.ErrNoRows), "want a constraint violation")
		})
	}
}

// The companion to the test above: a constraint that refused everything would
// pass it. All five declared states must still be storable, spelled exactly as
// the Go constants spell them — the column and the constants have to agree, or
// a cached verdict becomes permanently unreachable and every render spends a
// lookup.
func TestAttachmentReputation_AcceptsAllFiveStates(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	for _, state := range []reputation.State{
		reputation.Unseen, reputation.Unscanned, reputation.Clean,
		reputation.Detected, reputation.Unavailable,
	} {
		t.Run(string(state), func(t *testing.T) {
			q, rollback := testutil.TxQueries(t, db)
			defer rollback()

			got, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
				Sha256:   repHashA,
				Provider: reputation.ProviderVirusTotal,
				State:    string(state),
			})
			require.NoError(t, err)
			require.Equal(t, string(state), got.State)
		})
	}
}

// Both provider names must be storable, spelled as internal/reputation spells
// them. The column and the attachment_reputation_provider setting have to
// agree on the spelling; this is the half of that agreement the database can
// hold up.
func TestAttachmentReputation_AcceptsBothProviderNames(t *testing.T) {
	q := repQueries(t)
	ctx := context.Background()

	for _, p := range []string{reputation.ProviderVirusTotal, reputation.ProviderMetaDefender} {
		got, err := q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
			Sha256: repHashB, Provider: p, State: string(reputation.Unseen),
		})
		require.NoError(t, err)
		require.Equal(t, p, got.Provider)
	}
}
