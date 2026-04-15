// Package testutil provides helpers for integration tests that require a real
// PostgreSQL database. Tests are skipped when TEST_DATABASE_URL is not set.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// DB holds a test database connection and a rolled-back transaction per test.
type DB struct {
	Pool    *pgxpool.Pool
	SQL     *sql.DB
	Queries *dbgen.Queries
}

// NewDB opens a connection to TEST_DATABASE_URL, runs all migrations, and
// returns a DB. The test is skipped when the env var is not set.
// Call t.Cleanup on the returned closer.
func NewDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	ctx := context.Background()
	if err := database.Migrate(ctx, database.MigrateURL(dsn)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := database.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open db pool: %v", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbgen.New(sqlDB)

	cleanup := func() {
		sqlDB.Close()
		pool.Close()
	}
	return &DB{Pool: pool, SQL: sqlDB, Queries: q}, cleanup
}

// TxQueries wraps each test in a transaction that is rolled back on cleanup,
// giving full isolation between tests without truncating tables.
func TxQueries(t *testing.T, db *DB) (*dbgen.Queries, func()) {
	t.Helper()
	tx, err := db.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	return dbgen.New(tx), func() { _ = tx.Rollback() }
}
