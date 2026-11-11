package replyindex_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The background fill and index build behind reply search. Each test runs in a
// database of its own: the fill commits for real, and the index build waits for
// every open transaction, so it cannot share a database with other packages'
// tests.

func newPreparer(db *sql.DB) *replyindex.Preparer {
	p := replyindex.New(db, slog.Default())
	p.BatchSize = 3
	p.Pause = 0
	return p
}

// replies puts n committed replies on one ticket and blanks their body_tsv, the
// state a reply written before the migration is in.
func replies(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO categories (id, name, sort_order) VALUES ('00000000-0000-0000-0000-0000000000aa', 'fixture', 1) ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	var tk string
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO tickets (tracking_number, subject, category_id, status_id, guest_email, description_format, source_channel)
		SELECT 'FIX-' || substr(md5(random()::text), 1, 8), 'fixture', '00000000-0000-0000-0000-0000000000aa', id, 'g@example.com', 'plain', 'web'
		FROM statuses WHERE name = 'New' RETURNING id`).Scan(&tk))
	_, err = db.ExecContext(ctx, `INSERT INTO ticket_replies (ticket_id, body, internal, created_at, body_format)
		SELECT $1, 'reply number ' || n || ' about the fuser', false, now() - (n || ' minutes')::interval, 'plain' FROM generate_series(1, $2) n`, tk, n)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE ticket_replies SET body_tsv = NULL`)
	require.NoError(t, err)
}

func nullCount(t *testing.T, db *sql.DB) (n int) {
	t.Helper()
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM ticket_replies WHERE body_tsv IS NULL`).Scan(&n))
	return n
}

func indexValid(t *testing.T, db *sql.DB, name string) (exists, valid bool) {
	t.Helper()
	err := db.QueryRow(`SELECT i.indisvalid FROM pg_index i WHERE i.indexrelid = to_regclass($1)`, name).Scan(&valid)
	if err == sql.ErrNoRows {
		return false, false
	}
	require.NoError(t, err)
	return true, valid
}

func TestPrepare_FillsInBatchesAndBuildsBothIndexes(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 10)

	ready, err := replyindex.Ready(ctx, db.SQL)
	require.NoError(t, err)
	require.False(t, ready, "nothing is ready before the fill and the build")

	res, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Done)
	require.False(t, res.Locked)
	require.EqualValues(t, 10, res.Filled)
	require.GreaterOrEqual(t, res.Batches, 4, "10 rows in batches of 3")
	require.Zero(t, nullCount(t, db.SQL), "every existing reply has a search column")

	for _, name := range []string{"ticket_replies_body_tsv_idx", "ticket_replies_created_at_idx"} {
		exists, valid := indexValid(t, db.SQL, name)
		require.True(t, exists && valid, name)
	}
	ready, err = replyindex.Ready(ctx, db.SQL)
	require.NoError(t, err)
	require.True(t, ready)

	// The filled column is what the search expression would have produced.
	var mismatched int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM ticket_replies WHERE body_tsv IS DISTINCT FROM to_tsvector('english', body)`).Scan(&mismatched))
	require.Zero(t, mismatched)
}

// A second run has nothing to do and touches no row.
func TestPrepare_IsIdempotentAndLeavesFilledRowsAlone(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 5)
	_, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)

	var xminBefore string
	require.NoError(t, db.SQL.QueryRow(`SELECT string_agg(xmin::text, ',' ORDER BY id) FROM ticket_replies`).Scan(&xminBefore))
	res, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Done)
	require.Zero(t, res.Filled)
	var xminAfter string
	require.NoError(t, db.SQL.QueryRow(`SELECT string_agg(xmin::text, ',' ORDER BY id) FROM ticket_replies`).Scan(&xminAfter))
	require.Equal(t, xminBefore, xminAfter, "no row was rewritten")
}

// A restart resumes: what the first run filled stays filled, and the second run
// finishes the rest.
func TestPrepare_ResumesAfterAnInterruption(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	replies(t, db.SQL, 10)

	ctx, cancel := context.WithCancel(context.Background())
	p := newPreparer(db.SQL)
	p.OnBatch = func(filled int64) { cancel() } // stop after the first batch
	res, err := p.Prepare(ctx)
	require.Error(t, err, "an interrupted run says so")
	require.False(t, res.Done)
	left := nullCount(t, db.SQL)
	require.Greater(t, left, 0)
	require.Less(t, left, 10, "the first batch is committed")

	res, err = newPreparer(db.SQL).Prepare(context.Background())
	require.NoError(t, err)
	require.True(t, res.Done)
	require.EqualValues(t, left, res.Filled, "only what was left")
	require.Zero(t, nullCount(t, db.SQL))
}

// Another replica (or a second Prepare) holding the lock means this one does
// nothing, rather than filling the same rows twice.
func TestPrepare_OnlyOneRunnerAtATime(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 4)

	conn, err := db.SQL.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, replyindex.LockKey)
	require.NoError(t, err)

	res, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Locked)
	require.False(t, res.Done)
	require.Equal(t, 4, nullCount(t, db.SQL), "the runner that did not get the lock filled nothing")

	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, replyindex.LockKey)
	require.NoError(t, err)
	res, err = newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Done)
}

// A CREATE INDEX CONCURRENTLY that failed leaves an INVALID index under its
// name, and IF NOT EXISTS would skip it without a word. Search would then never
// switch on, or worse scan the table. It is dropped and built again.
func TestPrepare_ReplacesAnInvalidIndex(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 3)
	_, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)

	var oid uint32
	require.NoError(t, db.SQL.QueryRow(`SELECT 'ticket_replies_body_tsv_idx'::regclass::oid::int8`).Scan(&oid))
	_, err = db.SQL.Exec(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'ticket_replies_body_tsv_idx'::regclass`)
	require.NoError(t, err)
	ready, err := replyindex.Ready(ctx, db.SQL)
	require.NoError(t, err)
	require.False(t, ready, "an invalid index is not ready")

	res, err := newPreparer(db.SQL).Prepare(ctx)
	require.NoError(t, err)
	require.True(t, res.Done)
	exists, valid := indexValid(t, db.SQL, "ticket_replies_body_tsv_idx")
	require.True(t, exists && valid)
	var oid2 uint32
	require.NoError(t, db.SQL.QueryRow(`SELECT 'ticket_replies_body_tsv_idx'::regclass::oid::int8`).Scan(&oid2))
	require.NotEqual(t, oid, oid2, "a new index, not the old one revalidated")
	ready, err = replyindex.Ready(ctx, db.SQL)
	require.NoError(t, err)
	require.True(t, ready)
}

// Run is what the server starts. It returns, without being asked, once the
// search is ready.
func TestRun_ReturnsWhenReady(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	replies(t, db.SQL, 7)

	p := newPreparer(db.SQL)
	p.Retry = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("Run did not finish")
	}
	ready, err := replyindex.Ready(context.Background(), db.SQL)
	require.NoError(t, err)
	require.True(t, ready)
	require.Zero(t, nullCount(t, db.SQL))
}

// A write that lands while the fill is running is not blocked by it and ends up
// filled: the trigger does it, whichever side of the cursor it falls on.
func TestPrepare_ConcurrentInsertsAreFilled(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	replies(t, db.SQL, 9)

	var tk string
	require.NoError(t, db.SQL.QueryRow(`SELECT id FROM tickets LIMIT 1`).Scan(&tk))
	p := newPreparer(db.SQL)
	inserted := 0
	p.OnBatch = func(int64) {
		inserted++
		_, err := db.SQL.Exec(`INSERT INTO ticket_replies (ticket_id, body, internal, created_at, body_format) VALUES ($1, 'arrived during the fill', false, now(), 'plain')`, tk)
		require.NoError(t, err)
	}
	res, err := p.Prepare(context.Background())
	require.NoError(t, err)
	require.True(t, res.Done)
	require.Greater(t, inserted, 0)
	require.Zero(t, nullCount(t, db.SQL), "the inserts have a search column too")
}

// The fill reads the table in the order it lies on disk, not in primary-key
// order. Reply ids are random UUIDs, so key order visits one row per heap page
// and, once the table is bigger than memory, every batch is a thousand random
// reads: measured at 5,000,000 replies on a busy machine, 1.5 s for a batch
// that a physical-order read does in 20 ms. Here the ids run backwards against
// the insertion order, so a fill in key order would finish with the LAST rows
// written and one in physical order with the FIRST.
func TestPrepare_FillsInPhysicalOrderNotKeyOrder(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 1) // the ticket and category the rows need
	_, err := db.SQL.Exec(`DELETE FROM ticket_replies`)
	require.NoError(t, err)
	_, err = db.SQL.Exec(`INSERT INTO ticket_replies (id, ticket_id, body, internal, created_at, body_format)
		SELECT ('00000000-0000-0000-0000-' || lpad((1000 - n)::text, 12, '0'))::uuid, (SELECT id FROM tickets LIMIT 1),
		       'reply number ' || n, false, now(), 'plain'
		FROM generate_series(1, 10) n`)
	require.NoError(t, err)
	_, err = db.SQL.Exec(`UPDATE ticket_replies SET body_tsv = NULL`)
	require.NoError(t, err)

	pc, cancel := context.WithCancel(ctx)
	p := newPreparer(db.SQL)
	p.OnBatch = func(int64) { cancel() } // stop after the first batch of three
	_, err = p.Prepare(pc)
	require.Error(t, err)

	rows, err := db.SQL.Query(`SELECT body FROM ticket_replies WHERE body_tsv IS NOT NULL ORDER BY body`)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var b string
		require.NoError(t, rows.Scan(&b))
		got = append(got, b)
	}
	require.Equal(t, []string{"reply number 1", "reply number 2", "reply number 3"}, got,
		"the first batch is the first three rows written")
}

// A row the fill has already passed can be left without a column (a write that
// raced the batch that was updating its page, say). Search switches on only
// when the column is complete, so the fill looks again before building the
// indexes. Here the last batch blanks the first row, which is behind the
// cursor by then.
func TestPrepare_RechecksForRowsLeftBehind(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	replies(t, db.SQL, 10)

	p := newPreparer(db.SQL)
	blanked := false
	p.OnBatch = func(filled int64) {
		if filled == 10 && !blanked {
			blanked = true
			_, err := db.SQL.Exec(`UPDATE ticket_replies SET body_tsv = NULL WHERE body = 'reply number 1 about the fuser'`)
			require.NoError(t, err)
		}
	}
	res, err := p.Prepare(context.Background())
	require.NoError(t, err)
	require.True(t, blanked)
	require.True(t, res.Done)
	require.Zero(t, nullCount(t, db.SQL), "the row that was left behind was filled before the indexes were built")
}

// The GIN build is the long step, and Postgres' default maintenance_work_mem
// (64 MB) makes it flush its in-memory list into the index far more often than
// it needs to: at 5,000,000 replies a build with 1 GB was done in 47 minutes
// while one with the default was 14% through after 16. The job gives its own
// session 256 MB for the build, and only that session: a setting left on a
// pooled connection would apply to whatever query the server ran on it next.
func TestPrepare_BuildsWithItsOwnMaintenanceWorkMem(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	replies(t, db.SQL, 5)

	var serverDefault string
	require.NoError(t, db.SQL.QueryRow(`SHOW maintenance_work_mem`).Scan(&serverDefault))
	require.NotEqual(t, "256MB", serverDefault, "the check below would prove nothing")

	var seen []string
	p := newPreparer(db.SQL)
	p.OnBuild = func(index, maintenanceWorkMem string) { seen = append(seen, index+"="+maintenanceWorkMem) }
	_, err := p.Prepare(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{
		"ticket_replies_created_at_idx=256MB",
		"ticket_replies_body_tsv_idx=256MB",
	}, seen, "each build ran in a session set to 256MB")

	// Nothing leaked: every connection the pool can hand out has the default.
	db.SQL.SetMaxOpenConns(8)
	var conns []*sql.Conn
	defer func() { // also when an assertion fails: closeDB waits for open connections
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		c, err := db.SQL.Conn(ctx)
		require.NoError(t, err)
		conns = append(conns, c)
		var got string
		require.NoError(t, c.QueryRowContext(ctx, `SHOW maintenance_work_mem`).Scan(&got))
		require.Equal(t, serverDefault, got, "connection %d", i)
	}
}

// A size Postgres does not understand fails the build instead of being ignored.
func TestPrepare_RefusesAMaintenanceWorkMemItCannotSet(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	replies(t, db.SQL, 2)
	p := newPreparer(db.SQL)
	p.MaintenanceWorkMem = "a lot"
	_, err := p.Prepare(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "maintenance_work_mem")
}
