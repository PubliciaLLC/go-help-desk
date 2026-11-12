// Package ratelimitstore implements ratelimit.Store against PostgreSQL via sqlc.
package ratelimitstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/ratelimit"
)

// SweepBatch is how many expired rows one DELETE removes. See DeleteExpired.
const SweepBatch = 5000

// Store implements ratelimit.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Take(ctx context.Context, key string, window time.Duration, now time.Time) (ratelimit.Count, error) {
	row, err := s.q.TakeRateLimit(ctx, dbgen.TakeRateLimitParams{
		Key: key, Now: now, WindowSeconds: window.Seconds(),
	})
	if err != nil {
		return ratelimit.Count{}, fmt.Errorf("taking a rate limit slot: %w", err)
	}
	return ratelimit.Count{N: int(row.Count), WindowStart: row.WindowStart, WindowEnd: row.WindowEnd}, nil
}

func (s *Store) Refund(ctx context.Context, key string, windowStart time.Time) error {
	if err := s.q.RefundRateLimit(ctx, dbgen.RefundRateLimitParams{Key: key, WindowStart: windowStart}); err != nil {
		return fmt.Errorf("refunding a rate limit slot: %w", err)
	}
	return nil
}

func (s *Store) Spent(ctx context.Context, key string, now time.Time) (int, error) {
	n, err := s.q.GetRateLimitCount(ctx, dbgen.GetRateLimitCountParams{Key: key, Now: now})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading a rate limit count: %w", err)
	}
	return int(n), nil
}

func (s *Store) Reset(ctx context.Context, key string) error {
	if err := s.q.ResetRateLimit(ctx, key); err != nil {
		return fmt.Errorf("resetting a rate limit: %w", err)
	}
	return nil
}

// DeleteExpired removes every row whose window ended at or before now, and
// returns how many. A key that is used again after its window ended is replaced
// in place by Take, so this only has to find the ones nobody came back to.
//
// In batches of SweepBatch, each its own statement: a burst of attempts on
// made-up email addresses leaves a large backlog, and one DELETE of all of it
// would hold its locks and write its log in a single lump. A failure part way
// returns what was removed so far with the error; the next sweep carries on.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for {
		n, err := s.q.DeleteExpiredRateLimits(ctx, dbgen.DeleteExpiredRateLimitsParams{Now: now, Batch: SweepBatch})
		total += n
		if err != nil {
			return total, fmt.Errorf("sweeping expired rate limits: %w", err)
		}
		if n < SweepBatch {
			return total, nil
		}
	}
}
