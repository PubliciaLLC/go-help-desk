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
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

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

func (s *Store) DeleteFailedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteFailedNotificationsBefore(ctx, sql.NullTime{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("deleting failed notifications: %w", err)
	}
	return n, nil
}

var _ notification.Outbox = (*Store)(nil)
