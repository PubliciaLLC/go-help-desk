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
