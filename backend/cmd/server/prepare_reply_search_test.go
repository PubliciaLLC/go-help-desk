package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
)

func TestParsePrepareReplySearchFlags(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		o, err := parsePrepareReplySearchFlags(nil)
		require.NoError(t, err)
		require.Equal(t, "1GB", o.MaintenanceWorkMem, "the owner's default: 1 GB, overridable")
		require.EqualValues(t, 30, o.ProgressInterval.Seconds())
		require.Equal(t, 1000, o.BatchSize)
	})
	t.Run("overrides", func(t *testing.T) {
		o, err := parsePrepareReplySearchFlags([]string{"--maintenance-work-mem", "2GB", "--progress-interval", "5s", "--batch-size", "200"})
		require.NoError(t, err)
		require.Equal(t, "2GB", o.MaintenanceWorkMem)
		require.EqualValues(t, 5, o.ProgressInterval.Seconds())
		require.Equal(t, 200, o.BatchSize)
	})
	for name, args := range map[string][]string{
		"an unknown flag":               {"--nope"},
		"a duration that is not one":    {"--progress-interval", "often"},
		"a progress interval of zero":   {"--progress-interval", "0s"},
		"a batch size of zero":          {"--batch-size", "0"},
		"an argument that is no flag":   {"stray"},
		"an empty maintenance_work_mem": {"--maintenance-work-mem", ""},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := parsePrepareReplySearchFlags(args)
			require.Error(t, err)
		})
	}
}

// reply search as 1.2.0 left it: replies with no search column at all, and the
// feature's column and trigger absent.
func oneTwoDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := freshDatabase(t)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO categories (id, name, sort_order) VALUES ('00000000-0000-0000-0000-0000000000aa', 'fixture', 1) ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	_, err = db.Exec(`WITH t AS (INSERT INTO tickets (tracking_number, subject, category_id, status_id, guest_email, description_format, source_channel)
		SELECT 'FIX-' || substr(md5(random()::text), 1, 8), 'fixture', '00000000-0000-0000-0000-0000000000aa', id, 'g@example.com', 'plain', 'web'
		FROM statuses WHERE name = 'New' RETURNING id)
		INSERT INTO ticket_replies (ticket_id, body, internal, created_at, body_format)
		SELECT t.id, 'reply number ' || n || ' about the fuser', false, now() - (n || ' minutes')::interval, 'plain' FROM t, generate_series(1, 25) n`)
	require.NoError(t, err)
	down, err := os.ReadFile("../../internal/database/migrations/000062_reply_search.down.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	// The test database is migrated to the newest version, which is the migration
	// the command must NOT record. A 1.2 database is at 21; put it there, so a
	// command that wrote the version would change the number.
	_, err = db.Exec(`UPDATE schema_migrations SET version = 21`)
	require.NoError(t, err)
	return db
}

func testOptions() prepareOptions {
	o, _ := parsePrepareReplySearchFlags(nil)
	o.BatchSize = 7
	return o
}

func TestPrepareReplySearch_AppliesTheSchemaFillsAndBuildsWithoutRecordingTheMigration(t *testing.T) {
	db := oneTwoDatabase(t)
	var versionBefore int64
	require.NoError(t, db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&versionBefore))
	require.EqualValues(t, 21, versionBefore)

	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 0, code, out.String())
	require.Contains(t, out.String(), "reply search is ready")
	require.NotContains(t, out.String(), "already prepared", "this run did the work")
	require.Contains(t, out.String(), "maintenance_work_mem 1GB", "each build says what it really runs with")
	require.Contains(t, out.String(), "filling", "progress is printed")

	ready, err := replyindex.Ready(context.Background(), db)
	require.NoError(t, err)
	require.True(t, ready)
	var left int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM ticket_replies WHERE body_tsv IS NULL`).Scan(&left))
	require.Zero(t, left)
	var versionAfter int64
	require.NoError(t, db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&versionAfter))
	require.Equal(t, versionBefore, versionAfter, "1.2 must not see a migration version it does not know")
}

func TestPrepareReplySearch_TheMemoryOverrideIsUsed(t *testing.T) {
	db := oneTwoDatabase(t)
	o := testOptions()
	o.MaintenanceWorkMem = "512MB"
	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, o, &out)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	// What each build reports is the setting its own session has, read back from
	// Postgres, not the flag.
	require.Contains(t, out.String(), "ticket_replies_body_tsv_idx with maintenance_work_mem 512MB")
	require.NotContains(t, out.String(), "maintenance_work_mem 1GB")
}

// The flag only ever raises the setting: an administrator's larger value stays,
// and the output says what the build really uses.
func TestPrepareReplySearch_AServerSettingLargerThanTheFlagStays(t *testing.T) {
	db := oneTwoDatabase(t)
	var name string
	require.NoError(t, db.QueryRow(`SELECT current_database()`).Scan(&name))
	_, err := db.Exec(`ALTER DATABASE ` + name + ` SET maintenance_work_mem = '2GB'`)
	require.NoError(t, err)
	db.SetMaxIdleConns(0) // new connections see the new default
	o := testOptions()
	o.MaintenanceWorkMem = "128MB"
	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, o, &out)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "maintenance_work_mem 2GB")
	require.NotContains(t, out.String(), "maintenance_work_mem 128MB")
}

func TestPrepareReplySearch_RunAgainSaysThereIsNothingToDo(t *testing.T) {
	db := oneTwoDatabase(t)
	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	out.Reset()
	code, err = prepareReplySearch(context.Background(), db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "already prepared")
}

// The server's own job, or a second copy of this command, holds the same
// advisory lock, so the two cannot both run: exit code 3, and nothing is done.
func TestPrepareReplySearch_AnotherRunnerHoldsTheLock(t *testing.T) {
	db := oneTwoDatabase(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, replyindex.LockKey)
	require.NoError(t, err)

	// ...and, as a build does, a lock on the table that the DDL's ALTER TABLE
	// would queue behind. A second run must exit at once, without touching it.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`LOCK TABLE ticket_replies IN SHARE UPDATE EXCLUSIVE MODE`)
	require.NoError(t, err)

	limited, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	code, err := prepareReplySearch(limited, db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 3, code)
	require.Less(t, time.Since(start), 3*time.Second, "at once, not after waiting for the table")
	require.Contains(t, out.String(), "already being prepared")
	var column bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ticket_replies' AND column_name = 'body_tsv')`).Scan(&column))
	require.False(t, column, "nothing was touched: not even the column was added")
}

func TestPrepareReplySearch_AnInvalidMemorySizeIsAnError(t *testing.T) {
	db := oneTwoDatabase(t)
	o := testOptions()
	o.MaintenanceWorkMem = "a lot"
	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, o, &out)
	require.Error(t, err)
	require.Equal(t, 1, code)
}

// The command needs the database and nothing else. The server's own settings
// (BASE_URL, SESSION_SECRET and the rest) are for a server; asking for them made
// the documented command fail for anyone who did not copy the whole environment.
func TestPrepareReplySearchCommand_NeedsOnlyDATABASE_URL(t *testing.T) {
	dsn := freshDatabase(t)
	for _, k := range []string{"BASE_URL", "SESSION_SECRET", "JWT_SECRET", "SMTP_HOST", "SMTP_FROM"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("DATABASE_URL", dsn)
	var out bytes.Buffer
	require.Equal(t, 0, prepareReplySearchCommand(context.Background(), nil, &out), out.String())
	require.Contains(t, out.String(), "reply search is ready")
}

func TestPrepareReplySearchCommand_WithoutDATABASE_URL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	os.Unsetenv("DATABASE_URL")
	var out bytes.Buffer
	require.Equal(t, 1, prepareReplySearchCommand(context.Background(), nil, &out))
	require.Contains(t, out.String(), "DATABASE_URL")
}

func TestPrepareReplySearchCommand_UsageAndExitCodes(t *testing.T) {
	var out bytes.Buffer
	require.Equal(t, 2, prepareReplySearchCommand(context.Background(), []string{"--nope"}, &out), "a bad flag is exit 2")
	require.Contains(t, out.String(), "usage:")
	out.Reset()
	require.Equal(t, 0, prepareReplySearchCommand(context.Background(), []string{"--help"}, &out), "asking for help is not an error")
	require.Contains(t, out.String(), "usage:")

	// The usage text gives a form that works. The image's entrypoint is already
	// the server binary, so a command line that repeats it starts the server.
	u := prepareReplySearchUsage
	require.Contains(t, u, "docker run --rm --network")
	require.Contains(t, u, "DATABASE_URL")
	require.Contains(t, u, "Exit codes")
	require.False(t, strings.Contains(u, "/app/server prepare-reply-search"), "that form starts a full server in the image")
}
