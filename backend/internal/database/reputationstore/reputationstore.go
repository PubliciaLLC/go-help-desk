// Package reputationstore implements reputation.Store against PostgreSQL via
// sqlc.
package reputationstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// Store implements reputation.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// Compile-time proof that the adapter satisfies the contract the service
// expects, so a change to either side fails here rather than at the wiring.
var _ reputation.Store = (*Store)(nil)

// Get returns the cached verdict, or ErrNotCached when there is none.
//
// The sentinel is the point of this method. reputation.Service branches on it
// to decide whether to spend a lookup, and any other error — sql.ErrNoRows
// included — is read as "the cache is broken", which reports Unavailable and
// never calls the provider at all. A miss that does not arrive as ErrNotCached
// is therefore a feature that silently never works.
func (s *Store) Get(ctx context.Context, sha256, provider string) (reputation.Reputation, error) {
	row, err := s.q.GetAttachmentReputation(ctx, dbgen.GetAttachmentReputationParams{
		Sha256:   sha256,
		Provider: provider,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return reputation.Reputation{}, reputation.ErrNotCached
	}
	if err != nil {
		return reputation.Reputation{}, fmt.Errorf("reading %s verdict: %w", provider, err)
	}

	rep := reputation.Reputation{
		State: reputation.State(row.State),
		// NULL arrives as 0, which is correct for the states that carry no
		// numbers: they are only ever read alongside the State, and the State
		// is what says whether a count means anything.
		Detected:   int(row.Detected.Int32),
		Total:      int(row.Total.Int32),
		ThreatName: row.ThreatName.String,
		// The feeds that carry a "known" file. NOT NULL in the column and
		// empty on every other state, so nothing here has to distinguish "no
		// feeds" from "not recorded": an empty list cannot be misread as a
		// finding the way an engine count of zero can.
		KnownFeeds: row.KnownFeeds,
		// When WE fetched, as against when the provider analysed. NOT NULL in
		// the column and always set, because there is no row without a lookup
		// behind it — and it is what both refresh rules are decided on, so a
		// verdict that arrives without it never expires and can never be
		// re-checked by hand.
		FetchedAt: row.FetchedAt,
	}
	if row.AnalysedAt.Valid {
		t := row.AnalysedAt.Time
		rep.AnalysedAt = &t
	}
	return rep, nil
}

// Put records a completed lookup.
//
// rep.FetchedAt is deliberately not passed: fetched_at is the database's own
// clock, one clock deciding one timeline, and every write re-stamps it — which
// is what makes a re-check that returns the same answer still count as a
// re-check. See the query.
func (s *Store) Put(ctx context.Context, sha256, provider string, rep reputation.Reputation) error {
	var detected, total sql.NullInt32
	if hasCounts(rep.State) {
		// Only a completed analysis has numbers. For every other state the
		// provider gave us none, and writing 0 of 0 would render as "no engine
		// found anything" — the false reassurance this whole feature exists to
		// avoid. NULL says "not recorded" and the column is nullable for
		// exactly this.
		detected = sql.NullInt32{Int32: count(rep.Detected), Valid: true}
		total = sql.NullInt32{Int32: count(rep.Total), Valid: true}
	}

	var analysed sql.NullTime
	if rep.AnalysedAt != nil {
		analysed = sql.NullTime{Time: *rep.AnalysedAt, Valid: true}
	}

	_, err := s.q.UpsertAttachmentReputation(ctx, dbgen.UpsertAttachmentReputationParams{
		Sha256:     sha256,
		Provider:   provider,
		State:      string(rep.State),
		Detected:   detected,
		Total:      total,
		ThreatName: sql.NullString{String: rep.ThreatName, Valid: rep.ThreatName != ""},
		AnalysedAt: analysed,
		// Written on every state, not only "known". A provider only ever sets
		// these alongside Known, and writing what it gave us keeps the upsert
		// from carrying a previous verdict's feeds forward onto a verdict that
		// has none — which would attach the one positive claim this system can
		// make to a file nothing has on record.
		KnownFeeds: rep.KnownFeeds,
	})
	if err != nil {
		// The state and the provider are ours or the operator's; nothing here
		// carries the API key or the file's contents.
		return fmt.Errorf("caching %s verdict %q: %w", provider, rep.State, err)
	}
	return nil
}

// hasCounts reports whether a state has an engine count behind it.
func hasCounts(s reputation.State) bool {
	return s == reputation.Clean || s == reputation.Detected
}

// count narrows an engine count to the int32 the column holds.
//
// The value is decoded from a third party's JSON, so it is not ours to trust:
// a number past int32 would wrap to a negative "engines that ran". Neither
// bound is reachable from a real report — no service runs two billion engines —
// which is the point. A clamp here is cheaper than finding out.
func count(n int) int32 {
	if n < 0 {
		return 0
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(n)
}
