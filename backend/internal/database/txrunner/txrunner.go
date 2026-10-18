// Package txrunner binds the ticket and audit stores to a single database
// transaction, implementing ticket.Atomic.
//
// It is its own package rather than part of internal/database because
// ticketstore imports internal/database for its null-conversion helpers, so a
// runner living there would close an import cycle.
package txrunner

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"

	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Runner implements ticket.Atomic against a *sql.DB.
type Runner struct {
	db       *sql.DB
	channels []string
	wake     func()
}

// New returns a Runner over the given database handle. channels are the outbox
// channels a change's events are written for (the same list the request-side
// OutboxDispatcher queues for); none writes no rows, for a caller that does not
// announce. wake, if not nil, is called after a commit that wrote rows, so a
// worker in this process need not wait for its next poll.
func New(db *sql.DB, channels []string, wake func()) *Runner {
	return &Runner{db: db, channels: channels, wake: wake}
}

// InTx runs fn inside one transaction, passing it stores bound to that
// transaction and a collector for the change's events. It commits when fn
// returns nil and rolls back otherwise.
//
// The events fn raised are written to the outbox in the same transaction, just
// before the commit (#506): a rollback leaves no row, and a commit cannot leave
// a change unannounced. Before this the rows were written after the commit, on
// a context that could not be cancelled, and a crash between the two lost the
// notification.
func (r *Runner) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	// Retried once on a deadlock, and only on a deadlock.
	//
	// Two transactions can take the same two row locks in opposite orders,
	// and Postgres breaks the tie by killing one of them. The measured case:
	// an administrator assigns a ticket, which share-locks the assignee and
	// then writes an audit row whose actor is the administrator — while
	// another request disables or deletes somebody, which locks every
	// administrator row first and then the target. Each holds what the other
	// wants.
	//
	// A deadlock is not a fault in either transaction; it is the database
	// telling one of them to go again, and the one that goes again succeeds
	// because the other has finished. Reporting it as "an internal error
	// occurred" to somebody who assigned a ticket is the wrong answer to a
	// question that has a right one.
	//
	// Once, not in a loop: a second deadlock on the retry means something
	// structural, and hiding that behind repeated attempts would turn a bug
	// into a slow mystery.
	err := r.runOnce(ctx, fn)
	if isDeadlock(err) {
		return r.runOnce(ctx, fn)
	}
	return err
}

func (r *Runner) runOnce(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	// Rollback on every path that is not a successful commit, including a
	// panic. After a successful Commit this is a no-op that returns
	// sql.ErrTxDone, which is why the error is discarded here and nowhere else.
	defer func() { _ = tx.Rollback() }()

	q := dbgen.New(tx)
	// Created anew for every attempt, so a deadlock retry announces what the
	// attempt that succeeded did and nothing of the one that was rolled back.
	changes := &notification.Changes{}
	if err := fn(ticketstore.New(q), auditstore.New(q), changes); err != nil {
		return err
	}
	queued, err := r.enqueue(ctx, q, changes)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	if queued && r.wake != nil {
		r.wake()
	}
	return nil
}

// enqueue writes one row per channel for each event the change raised, on the
// transaction's own connection. It reports whether it wrote any.
func (r *Runner) enqueue(ctx context.Context, q *dbgen.Queries, changes *notification.Changes) (bool, error) {
	var queued bool
	for _, ev := range changes.Events() {
		body, err := notification.EncodeEvent(ev)
		if err != nil {
			return false, fmt.Errorf("encoding %s event: %w", ev.Type, err)
		}
		for _, ch := range r.channels {
			if !notification.Carries(ch, ev) {
				continue
			}
			if err := q.EnqueueNotification(ctx, dbgen.EnqueueNotificationParams{ID: uuid.New(), Channel: ch, Event: body}); err != nil {
				return false, fmt.Errorf("queueing %s notification: %w", ch, err)
			}
			queued = true
		}
	}
	return queued, nil
}

// isDeadlock reports whether Postgres killed this transaction to break a
// lock cycle. SQLSTATE 40P01, matched on the code rather than the message.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}
