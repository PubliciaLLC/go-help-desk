package testutil

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// NewIsolatedDB is NewDB on a database of the test's own, created from
// TEST_DATABASE_URL, migrated, and dropped at cleanup.
//
// For a test that has to COMMIT into a table other packages' tests also read or
// claim from, the notification outbox above all: a row committed there is seen,
// leased or deleted by whichever test runs beside it, and the reverse. A
// database of its own has no neighbours, so nothing needs scoping or cleaning up
// to keep two packages from deciding each other's results.
func NewIsolatedDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	name := "ghd_iso_" + strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		_ = admin.Close()
		t.Fatalf("create database: %v", err)
	}
	drop := func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	}
	u, err := url.Parse(dsn)
	if err != nil {
		drop()
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	own := u.String()

	var pool *pgxpool.Pool
	if pool, err = database.New(ctx, own); err != nil {
		drop()
		t.Fatalf("open db pool: %v", err)
	}
	if err := migrateAndVerify(ctx, pool, own, database.MigrationFiles()); err != nil {
		pool.Close()
		drop()
		t.Fatalf("%v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	return &DB{Pool: pool, SQL: sqlDB, Queries: dbgen.New(sqlDB)}, func() {
		sqlDB.Close()
		pool.Close()
		drop()
	}
}
