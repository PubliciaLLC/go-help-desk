package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// fetched_at is when WE fetched, and #168 turned it from a number shown to a
// person into the field two rules are decided on: a verdict past the
// configured interval is re-fetched, and a person may ask for a re-check once
// a week. Neither rule can work on a value the adapter drops on the floor.

// Get hands the fetch time back, and it is the row's own.
//
// Without this the service sees a zero time on every verdict, reads it as "age
// unknown", and nothing ever expires: the feature is switched on, configured,
// tested at every other layer, and silently does nothing.
func TestReputationStore_GetSurfacesTheFetchTime(t *testing.T) {
	s, q := newReputationStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Clean, Total: 78}))

	row, err := q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256: repHashA, Provider: reputation.ProviderVirusTotal,
	})
	require.NoError(t, err)

	got, err := s.Get(ctx, repHashA, reputation.ProviderVirusTotal)
	require.NoError(t, err)
	require.False(t, got.FetchedAt.IsZero(), "a stored verdict always has a fetch time")
	require.True(t, row.FetchedAt.Equal(got.FetchedAt),
		"the adapter must hand back the row's fetched_at, not a time of its own: row %v, got %v",
		row.FetchedAt, got.FetchedAt)
}

// A re-check that comes back with the same answer still moves fetched_at.
//
// This is the one that makes the manual control behave. If an unchanged
// verdict leaves the timestamp where it was, the "check again" button re-arms
// the instant it is used and the next reader spends another lookup learning
// the same nothing.
func TestReputationStore_AnUnchangedVerdictStillMovesTheFetchTime(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	same := reputation.Reputation{State: reputation.Clean, Total: 78}
	require.NoError(t, s.Put(ctx, repHashB, reputation.ProviderVirusTotal, same))
	first, err := s.Get(ctx, repHashB, reputation.ProviderVirusTotal)
	require.NoError(t, err)

	require.NoError(t, s.Put(ctx, repHashB, reputation.ProviderVirusTotal, same))
	second, err := s.Get(ctx, repHashB, reputation.ProviderVirusTotal)
	require.NoError(t, err)

	require.Truef(t, second.FetchedAt.After(first.FetchedAt),
		"the second write left fetched_at at %v, so the row still looks as old as it did before the re-check",
		first.FetchedAt)
}

// The fetch time is the database's clock and not the caller's.
//
// Reputation.FetchedAt is read-side: it is filled in on the way out of the
// store and ignored on the way in. A caller that sets it — a provider parsing
// a date out of a response, a test building a fixture — must not be able to
// backdate a row into permanent staleness, or forward-date one out of reach of
// the next re-check.
func TestReputationStore_PutIgnoresACallerSuppliedFetchTime(t *testing.T) {
	s, _ := newReputationStore(t)
	ctx := context.Background()

	backdated := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.Put(ctx, repHashA, reputation.ProviderVirusTotal,
		reputation.Reputation{State: reputation.Clean, Total: 78, FetchedAt: backdated}))

	got, err := s.Get(ctx, repHashA, reputation.ProviderVirusTotal)
	require.NoError(t, err)
	require.True(t, got.FetchedAt.After(backdated.AddDate(20, 0, 0)),
		"the caller's fetch time was written to the row: got %v", got.FetchedAt)
}
