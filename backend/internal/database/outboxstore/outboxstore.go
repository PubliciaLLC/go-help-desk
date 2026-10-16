// Package outboxstore implements domain/notification.Outbox against PostgreSQL.
package outboxstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Store implements notification.Outbox.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // for Split's transaction; nil when the Queries are already in one
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB returns a Store over a database handle, which also lets it Split a
// change's row in one transaction.
func NewWithDB(db *sql.DB) *Store { return &Store{q: dbgen.New(db), db: db} }

// Split queues rows and deletes the parent row in one transaction: all or
// nothing (notification.Splitter). Without a database handle it falls back to
// the same steps one by one, which is what a store already inside a
// transaction (a test's) does anyway.
func (s *Store) Split(ctx context.Context, parent uuid.UUID, rows []notification.NewRow) error {
	q, finish := s.q, func(error) error { return nil }
	if s.db != nil {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning split: %w", err)
		}
		q = s.q.WithTx(tx)
		finish = func(err error) error {
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if cerr := tx.Commit(); cerr != nil {
				return fmt.Errorf("committing split: %w", cerr)
			}
			return nil
		}
	}
	var err error
	for _, r := range rows {
		if err = q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{ID: r.ID, Channel: r.Channel, Event: r.Event}); err != nil {
			err = fmt.Errorf("queueing split row: %w", err)
			break
		}
	}
	if err == nil {
		if err = q.DeleteNotification(ctx, parent); err != nil {
			err = fmt.Errorf("settling split row: %w", err)
		}
	}
	return finish(err)
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
