package database_test

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
)

// scratchDatabase gives the test an empty database of its own, so it can see a
// schema at a version it chooses. Skipped without TEST_DATABASE_URL, like every
// database test.
func scratchDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	name := "ghd_migrate_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	})
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// latestMigration is the highest version the embedded files carry.
func latestMigration(t *testing.T) uint {
	t.Helper()
	files, err := fs.ReadDir(database.MigrationFiles(), ".")
	require.NoError(t, err)
	var latest uint
	for _, f := range files {
		n, err := strconv.ParseUint(strings.SplitN(f.Name(), "_", 2)[0], 10, 32)
		require.NoError(t, err)
		if uint(n) > latest {
			latest = uint(n)
		}
	}
	return latest
}

// An upgrade says where it started and where it ended (#525). Before, the
// startup log was silent about the schema, so an operator could only learn that
// 30 migrations had run by querying schema_migrations.
func TestMigrateReport_SaysWhatRan(t *testing.T) {
	ctx := context.Background()
	latest := latestMigration(t)
	dsn := database.MigrateURL(scratchDatabase(t))

	// 1.2.0 shipped at schema version 21: bring the database there by hand,
	// then let the server's own path take it the rest of the way.
	src, err := iofs.New(database.MigrationFiles(), ".")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(21))
	_, _ = m.Close()

	got, err := database.MigrateReport(ctx, dsn)
	require.NoError(t, err)
	require.Equal(t, database.Migration{From: 21, To: latest}, got)
	require.Equal(t, "database migrated from 21 to "+strconv.Itoa(int(latest)), got.String())

	// Nothing left to do: say the schema is current, and which version.
	got, err = database.MigrateReport(ctx, dsn)
	require.NoError(t, err)
	require.Equal(t, database.Migration{From: latest, To: latest}, got)
	require.Equal(t, "database schema at "+strconv.Itoa(int(latest))+", no migrations needed", got.String())
}

func TestMigrateReport_NewDatabaseStartsFromZero(t *testing.T) {
	latest := latestMigration(t)
	got, err := database.MigrateReport(context.Background(), database.MigrateURL(scratchDatabase(t)))
	require.NoError(t, err)
	require.Equal(t, database.Migration{From: 0, To: latest}, got)
	require.Equal(t, "database migrated from 0 to "+strconv.Itoa(int(latest)), got.String())
}
