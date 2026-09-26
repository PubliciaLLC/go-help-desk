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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"

	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Runner implements ticket.Atomic against a *sql.DB.
type Runner struct {
	db *sql.DB
}

// New returns a Runner over the given database handle.
func New(db *sql.DB) *Runner { return &Runner{db: db} }

// InTx runs fn inside one transaction, passing it stores bound to that
// transaction. It commits when fn returns nil and rolls back otherwise.
func (r *Runner) InTx(ctx context.Context, fn func(ticket.Store, audit.Store) error) error {
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

func (r *Runner) runOnce(ctx context.Context, fn func(ticket.Store, audit.Store) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	// Rollback on every path that is not a successful commit, including a
	// panic. After a successful Commit this is a no-op that returns
	// sql.ErrTxDone, which is why the error is discarded here and nowhere else.
	defer func() { _ = tx.Rollback() }()

	q := dbgen.New(tx)
	if err := fn(ticketstore.New(q), auditstore.New(q)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// isDeadlock reports whether Postgres killed this transaction to break a
// lock cycle. SQLSTATE 40P01, matched on the code rather than the message.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}
