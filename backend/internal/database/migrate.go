package database

import (
	"context"
	"embed"
	"fmt"
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

// Migrate runs all pending up migrations against the given database URL.
// It uses an advisory lock internally, so it is safe to call concurrently
// from multiple replicas at startup.
func Migrate(ctx context.Context, databaseURL string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("loading migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("creating migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}
