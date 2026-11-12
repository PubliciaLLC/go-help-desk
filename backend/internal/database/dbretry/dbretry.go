// Package dbretry is the one definition of when a database write that
// Postgres aborted is run again, shared by the transaction runner and the user
// store (#508).
package dbretry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Attempts is how many times the work is tried in all.
//
// One retry was not enough (#508): the aborted transaction releases the row
// the other one was queued for, and a retry that arrives before that
// transaction wakes takes the row first. That forms a second cycle in which
// the retry has waited longest, so it is killed again. Three because a cycle
// needs a second transaction that is both late and still unfinished, and each
// extra attempt needs another such transaction; a third abort in a row is a
// lock-order bug, and it is returned rather than hidden behind more waiting.
const Attempts = 3

// IsRetryable reports whether Postgres aborted the transaction to break a lock
// cycle (40P01, deadlock_detected) or to refuse a serialization conflict
// (40001, serialization_failure). Both mean "go again", and nothing else
// does: a constraint violation or a lost connection would fail the same way
// on the next attempt. Matched on the code, never the message.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

// Pause waits 10-50 ms, or until ctx is done. The wait lets the transaction
// that won wake, take its row and finish; the jitter keeps two retries from
// meeting again in step.
func Pause(ctx context.Context) error {
	select {
	case <-time.After(10*time.Millisecond + rand.N(40*time.Millisecond)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Do runs fn up to Attempts times, again only while it fails with a retryable
// error, pausing between attempts with pause (nil means Pause; a test passes
// its own to hold the next attempt until a concurrent transaction has
// finished). It returns fn's last error, or pause's if the context ended
// while waiting. fn must be safe to run again: each attempt starts from
// whatever the database holds now, and nothing of the last one may leak.
func Do(ctx context.Context, pause func(context.Context) error, fn func() error) error {
	if pause == nil {
		pause = Pause
	}
	var err error
	for attempt := 1; attempt <= Attempts; attempt++ {
		if err = fn(); !IsRetryable(err) || attempt == Attempts {
			return err
		}
		if perr := pause(ctx); perr != nil {
			return perr
		}
	}
	return err
}
