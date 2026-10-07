package notification

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// OutboxRow is one queued delivery: one event on one channel.
type OutboxRow struct {
	ID       uuid.UUID
	Channel  string
	Event    []byte // the serialised event; never holds a guest's raw token
	Attempts int    // including the claim that returned this row
}

// Outbox is the durable queue notifications are delivered from (#164).
// Requests enqueue and return; a worker claims, sends and settles each row.
type Outbox interface {
	Enqueue(ctx context.Context, id uuid.UUID, channel string, event []byte) error
	// Claim leases up to limit due rows to the caller for lease. A row whose
	// lease runs out without being settled is due again.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]OutboxRow, error)
	// Delete settles a delivered row.
	Delete(ctx context.Context, id uuid.UUID) error
	// Retry releases a row to be tried again at the given time.
	Retry(ctx context.Context, id uuid.UUID, at time.Time, lastErr string) error
	// Fail gives up on a row; it is kept for inspection until cleaned up.
	Fail(ctx context.Context, id uuid.UUID, lastErr string) error
	DeleteFailedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
