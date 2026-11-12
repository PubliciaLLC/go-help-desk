package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

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

	var out bytes.Buffer
	code, err := prepareReplySearch(context.Background(), db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 0, code, out.String())
	require.Contains(t, out.String(), "reply search is ready")
	require.Contains(t, out.String(), "maintenance_work_mem 1GB", "the build says what it runs with")
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
	require.Contains(t, out.String(), "maintenance_work_mem 512MB")
	require.NotContains(t, out.String(), "maintenance_work_mem 1GB")
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

	var out bytes.Buffer
	code, err := prepareReplySearch(ctx, db, testOptions(), &out)
	require.NoError(t, err)
	require.Equal(t, 3, code)
	require.Contains(t, out.String(), "already being prepared")
	var left int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM ticket_replies WHERE body_tsv IS NULL`).Scan(&left))
	require.Equal(t, 25, left, "nothing was filled")
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
