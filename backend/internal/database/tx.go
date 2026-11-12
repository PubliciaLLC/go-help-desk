package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// InTx runs fn in one transaction on db and hands it the queries bound to it.
//
// With a nil db it runs fn on q as it is. That is the integration harness,
// which wraps each test in a transaction it rolls back, over the Queries it
// gives every store: a second transaction on another connection could not see
// that one's rows (customfieldstore.New documents the same trade). A nil db
// therefore does not exercise commit or rollback, or any lock between two
// connections; the tests that need one open a real pool.
func InTx(ctx context.Context, db *sql.DB, q *dbgen.Queries, fn func(q *dbgen.Queries) error) error {
	if db == nil {
		return fn(q)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if err := fn(q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}
