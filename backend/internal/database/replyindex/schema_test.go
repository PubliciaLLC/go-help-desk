package replyindex_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The pre-build command (helpdesk server prepare-reply-search) is run against a
// 1.2 database that 1.2 is still serving. 1.2.0 refuses to start on a migration
// version it has no file for (golang-migrate: "no migration found for version
// 62"), but it tolerates the column and the trigger: its queries name their
// columns, and the trigger only sets the new one. So EnsureSchema applies the
// DDL of migration 000062 and leaves schema_migrations alone; when 1.3.0 starts
// it runs 000062 for real, which changes nothing because every statement in it
// is idempotent.

func migrationVersion(t *testing.T, db *sql.DB) (version int64, dirty bool) {
	t.Helper()
	require.NoError(t, db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty))
	return version, dirty
}

func hasColumnAndTrigger(t *testing.T, db *sql.DB) (column, trigger bool) {
	t.Helper()
	require.NoError(t, db.QueryRow(`SELECT
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'ticket_replies' AND column_name = 'body_tsv'),
		EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'ticket_replies_body_tsv_trg' AND NOT tgisinternal)`).Scan(&column, &trigger))
	return column, trigger
}

// dropReplySearch puts the database back as 1.2.0 left it, as far as this
// feature goes: the down file of migration 000062, and the version left alone.
func dropReplySearch(t *testing.T, db *sql.DB) {
	t.Helper()
	down, err := os.ReadFile("../migrations/000062_reply_search.down.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
}

func TestEnsureSchema_AppliesTheDDLAndLeavesTheMigrationVersionAlone(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	ddl, err := database.ReplySearchSchemaSQL()
	require.NoError(t, err)
	dropReplySearch(t, db.SQL)
	col, trg := hasColumnAndTrigger(t, db.SQL)
	require.False(t, col || trg, "precondition: a database without the feature")
	versionBefore, dirtyBefore := migrationVersion(t, db.SQL)

	p := newPreparer(db.SQL)
	require.NoError(t, p.EnsureSchema(ctx, ddl))
	col, trg = hasColumnAndTrigger(t, db.SQL)
	require.True(t, col, "the column")
	require.True(t, trg, "the trigger")
	versionAfter, dirtyAfter := migrationVersion(t, db.SQL)
	require.Equal(t, versionBefore, versionAfter, "schema_migrations is not touched: 1.2 would refuse to start on a version it does not know")
	require.Equal(t, dirtyBefore, dirtyAfter)

	// Idempotent, and the real migration file can run on top of it later.
	require.NoError(t, p.EnsureSchema(ctx, ddl))
	_, err = db.SQL.Exec(ddl)
	require.NoError(t, err, "1.3.0's own migration runs over the pre-build without error")

	// The trigger works.
	replies(t, db.SQL, 1)
	_, err = db.SQL.Exec(`INSERT INTO ticket_replies (ticket_id, body, internal, body_format) SELECT id, 'a fresh reply', false, 'plain' FROM tickets LIMIT 1`)
	require.NoError(t, err)
	var filled bool
	require.NoError(t, db.SQL.QueryRow(`SELECT body_tsv IS NOT NULL FROM ticket_replies WHERE body = 'a fresh reply'`).Scan(&filled))
	require.True(t, filled)
}

// After a pre-build the server's own job has nothing to do: Ready is true at its
// first look and Run returns at once.
func TestEnsureSchema_ThenPrepareLeavesNothingForTheServerJob(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	ddl, _ := database.ReplySearchSchemaSQL()
	replies(t, db.SQL, 6)
	dropReplySearch(t, db.SQL)

	p := newPreparer(db.SQL)
	require.NoError(t, p.EnsureSchema(ctx, ddl))
	res, err := p.Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Done)
	ready, err := replyindex.Ready(ctx, db.SQL)
	require.NoError(t, err)
	require.True(t, ready)

	// 1.3.0 starts: its migration re-applies, and the job finds nothing to do.
	_, err = db.SQL.Exec(ddl)
	require.NoError(t, err)
	job := newPreparer(db.SQL)
	job.Retry = time.Hour
	done := make(chan struct{})
	go func() { job.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server's job did not return at once on a prepared database")
	}
	require.Zero(t, nullCount(t, db.SQL))
}

// 1.2.0's own statements, as its generated code has them (dbgen/tickets.sql.go at
// v1.2.0): an INSERT naming its columns and a SELECT naming the same ones, never
// SELECT *. With the column and trigger present they still work. This database
// is at the 1.3 schema, which differs from 1.2's in two columns of this table:
// guest_token is gone (so it is left out here) and body_format is required (so
// the INSERT adds it). Neither has anything to do with body_tsv.
func TestEnsureSchema_ThePreBuiltColumnIsInvisibleToTheStatements1Point2Uses(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	ddl, _ := database.ReplySearchSchemaSQL()
	dropReplySearch(t, db.SQL)
	require.NoError(t, newPreparer(db.SQL).EnsureSchema(ctx, ddl))
	replies(t, db.SQL, 1)

	var ticketID string
	require.NoError(t, db.SQL.QueryRow(`SELECT id FROM tickets LIMIT 1`).Scan(&ticketID))
	_, err := db.SQL.Exec(`INSERT INTO ticket_replies (id, ticket_id, author_id, body, internal, notify_customer, created_at, body_format)
		VALUES (gen_random_uuid(), $1, NULL, 'written by the old version', false, true, now(), 'plain')`, ticketID)
	require.NoError(t, err)

	rows, err := db.SQL.Query(`SELECT id, ticket_id, author_id, body, internal, created_at, notify_customer FROM ticket_replies WHERE ticket_id = $1 ORDER BY created_at ASC`, ticketID)
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, tid string
		var author sql.NullString
		var body string
		var internal, notify bool
		var created time.Time
		require.NoError(t, rows.Scan(&id, &tid, &author, &body, &internal, &created, &notify), "a scan of exactly the named columns, as 1.2 does it")
		n++
	}
	require.NoError(t, rows.Err())
	require.GreaterOrEqual(t, n, 2)
}

// The DDL takes brief ACCESS EXCLUSIVE locks. On a busy 1.2 database it waits
// behind open transactions, so it gives up after a short lock_timeout and tries
// again, instead of queueing behind them and blocking every reply after it.
func TestEnsureSchema_RetriesWhileATransactionHoldsTheTable(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	ddl, _ := database.ReplySearchSchemaSQL()
	dropReplySearch(t, db.SQL)

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`LOCK TABLE ticket_replies IN ACCESS SHARE MODE`) // an ordinary reader: blocks ALTER TABLE
	require.NoError(t, err)
	released := time.AfterFunc(1500*time.Millisecond, func() { _ = tx.Rollback() })
	defer released.Stop()

	p := newPreparer(db.SQL)
	p.LockTimeout = 200 * time.Millisecond
	p.LockAttempts = 50
	start := time.Now()
	require.NoError(t, p.EnsureSchema(ctx, ddl), "it waits out the transaction")
	require.GreaterOrEqual(t, time.Since(start), time.Second)
	col, trg := hasColumnAndTrigger(t, db.SQL)
	require.True(t, col && trg)
}

func TestEnsureSchema_GivesUpWhenTheTableStaysLocked(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	ddl, _ := database.ReplySearchSchemaSQL()
	dropReplySearch(t, db.SQL)

	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`LOCK TABLE ticket_replies IN ACCESS SHARE MODE`)
	require.NoError(t, err)

	p := newPreparer(db.SQL)
	p.LockTimeout = 100 * time.Millisecond
	p.LockAttempts = 3
	// Bounded, so that without the lock_timeout this fails instead of waiting for
	// a transaction that never ends.
	limited, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = p.EnsureSchema(limited, ddl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gave up", "it was the lock timeout that ended it, not the deadline")
	col, trg := hasColumnAndTrigger(t, db.SQL)
	require.False(t, col || trg, "nothing was half applied")
}
