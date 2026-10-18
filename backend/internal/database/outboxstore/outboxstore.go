// Package outboxstore implements domain/notification.Outbox against PostgreSQL.
package outboxstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Store implements notification.Outbox.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // for Split's transaction; nil from New, which cannot Split
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB returns a Store over a database handle, which also lets it Split a
// change's row in one transaction.
func NewWithDB(db *sql.DB) *Store { return &Store{q: dbgen.New(db), db: db} }

// Split queues rows and deletes the parent row in one transaction: all or
// nothing (notification.Splitter), and only if this call is the one that took
// the parent. A store built with New has no database handle to open a
// transaction on, and doing the steps as separate commits would lose mail when
// an insert failed after the parent was gone, so it refuses instead.
func (s *Store) Split(ctx context.Context, parent uuid.UUID, rows []notification.NewRow) error {
	if s.db == nil {
		return errors.New("splitting a notification needs a store built with NewWithDB")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning split: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	q := s.q.WithTx(tx)

	// The parent goes first, and its deletion is the lock. A split that outlasts
	// the claim lease lets a second worker claim and split the same parent, and
	// the first split's children may be sent and gone by then, so ON CONFLICT
	// alone would let the second queue them again (#513). Whoever deletes zero
	// rows lost: another worker already split or settled it, so queue nothing.
	// A concurrent loser waits on the winner's row lock and then sees zero rows.
	took, err := q.TakeNotification(ctx, parent)
	if err != nil {
		return fmt.Errorf("settling split row: %w", err)
	}
	if took == 0 {
		return nil
	}
	for _, r := range rows {
		if err := q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{ID: r.ID, Channel: r.Channel, Event: r.Event}); err != nil {
			return fmt.Errorf("queueing split row: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing split: %w", err)
	}
	return nil
}

func (s *Store) Enqueue(ctx context.Context, id uuid.UUID, channel string, event []byte) error {
	if err := s.q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{ID: id, Channel: channel, Event: event}); err != nil {
		return fmt.Errorf("enqueueing notification: %w", err)
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, limit int, lease time.Duration) ([]notification.OutboxRow, error) {
	rows, err := s.q.ClaimNotifications(ctx, dbgen.ClaimNotificationsParams{
		LeaseSeconds: int32(lease / time.Second),
		PageLimit:    int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("claiming notifications: %w", err)
	}
	out := make([]notification.OutboxRow, len(rows))
	for i, r := range rows {
		out[i] = notification.OutboxRow{ID: r.ID, Channel: r.Channel, Event: r.Event, Attempts: int(r.Attempts)}
	}
	return out, nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteNotification(ctx, id); err != nil {
		return fmt.Errorf("deleting notification: %w", err)
	}
	return nil
}

func (s *Store) Retry(ctx context.Context, id uuid.UUID, at time.Time, lastErr string) error {
	err := s.q.RetryNotification(ctx, dbgen.RetryNotificationParams{
		ID: id, AvailableAt: at, LastError: sql.NullString{String: lastErr, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("rescheduling notification: %w", err)
	}
	return nil
}

func (s *Store) Fail(ctx context.Context, id uuid.UUID, lastErr string) error {
	if err := s.q.FailNotification(ctx, dbgen.FailNotificationParams{ID: id, LastError: sql.NullString{String: lastErr, Valid: true}}); err != nil {
		return fmt.Errorf("failing notification: %w", err)
	}
	return nil
}

// RewriteEvent replaces a row's event. See notify.EventRewriter.
func (s *Store) RewriteEvent(ctx context.Context, id uuid.UUID, event []byte) error {
	if err := s.q.RewriteNotificationEvent(ctx, dbgen.RewriteNotificationEventParams{ID: id, Event: event}); err != nil {
		return fmt.Errorf("rewriting notification event: %w", err)
	}
	return nil
}

func (s *Store) DeleteFailedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteFailedNotificationsBefore(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("deleting failed notifications: %w", err)
	}
	return n, nil
}

func (s *Store) EmailHealth(ctx context.Context) (notification.EmailHealth, error) {
	r, err := s.q.EmailOutboxHealth(ctx)
	if err != nil {
		return notification.EmailHealth{}, fmt.Errorf("reading email outbox health: %w", err)
	}
	return notification.EmailHealth{Failing: int(r.Failing), LastError: r.LastError}, nil
}

var (
	_ notification.Outbox       = (*Store)(nil)
	_ notification.Splitter     = (*Store)(nil)
	_ notification.HealthReader = (*Store)(nil)
)
