package report

import (
	"context"
	"time"
)

// Store is what the reports read through. It is implemented by
// *ticketstore.Store.
type Store interface {
	// Today is the local date of now in tz. A zone Postgres does not know is
	// ticket.ErrUnknownTimeZone.
	Today(ctx context.Context, tz string, now time.Time) (Day, error)
	// KPI runs every query of one request in one read-only REPEATABLE READ
	// transaction, so the rows are one snapshot.
	KPI(ctx context.Context, req Request) (Raw, error)
}
