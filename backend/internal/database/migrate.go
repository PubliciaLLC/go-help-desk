package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrateURL converts a standard postgres:// DSN to the pgx5:// scheme
// expected by the golang-migrate pgx/v5 driver.
func MigrateURL(dsn string) string {
	if strings.HasPrefix(dsn, "postgresql://") {
		return "pgx5://" + dsn[len("postgresql://"):]
	}
	if strings.HasPrefix(dsn, "postgres://") {
		return "pgx5://" + dsn[len("postgres://"):]
	}
	return dsn
}

// Migration says what a Migrate call found and where it left the schema.
// From is 0 for a database that had never been migrated.
type Migration struct {
	From, To uint
}

// String is the line the server logs at startup (#525): without it an operator
// could only learn that an upgrade had applied migrations by querying
// schema_migrations.
func (m Migration) String() string {
	if m.From == m.To {
		return fmt.Sprintf("database schema at %d, no migrations needed", m.To)
	}
	return fmt.Sprintf("database migrated from %d to %d", m.From, m.To)
}

// Migrate runs all pending up migrations against the given database URL.
// It uses an advisory lock internally, so it is safe to call concurrently
// from multiple replicas at startup.
func Migrate(ctx context.Context, databaseURL string) error {
	_, err := MigrateReport(ctx, databaseURL)
	return err
}

// MigrateReport is Migrate, and also reports the schema version it started at
// and the one it ended on. Domain-free code does not log; the caller does.
func MigrateReport(ctx context.Context, databaseURL string) (Migration, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return Migration{}, fmt.Errorf("loading migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return Migration{}, fmt.Errorf("creating migrator: %w", err)
	}
	defer m.Close()

	from, err := schemaVersion(m)
	if err != nil {
		return Migration{}, err
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return Migration{}, fmt.Errorf("running migrations: %w", err)
	}
	to, err := schemaVersion(m)
	if err != nil {
		return Migration{}, err
	}
	return Migration{From: from, To: to}, nil
}

// schemaVersion is the version the database is at, 0 when it has none yet.
func schemaVersion(m *migrate.Migrate) (uint, error) {
	v, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return v, nil
}

// MigrationFiles returns the embedded migration files, rooted at the
// migrations directory. testutil reads it to check that a shared test
// database was migrated by these files and not by another branch's.
func MigrationFiles() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err) // the directory is embedded; this cannot fail
	}
	return sub
}
