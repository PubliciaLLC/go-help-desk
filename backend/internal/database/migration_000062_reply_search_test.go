package database_test

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const (
	migration062Up   = "migrations/000062_reply_search.up.sql"
	migration062Down = "migrations/000062_reply_search.down.sql"
)

// applyMigration runs a migration file in a transaction and commits it, so the
// next catalog read, on any connection, sees the result.
func applyMigration(t *testing.T, ctx context.Context, db *sql.DB, path string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// A failed assertion in execMigrationFile ends the test without returning;
	// without this the connection stays checked out and the cleanup hangs.
	defer func() { _ = tx.Rollback() }()
	execMigrationFile(t, ctx, tx, path, nil)
	require.NoError(t, tx.Commit())
}

// repliesTable is what a test needs to put committed replies in an isolated
// database: one ticket to hang them on.
func repliesFixture(t *testing.T, ctx context.Context, db *sql.DB) (ticketID string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO categories (id, name, sort_order) VALUES ('00000000-0000-0000-0000-0000000000aa', 'fixture', 1)
		ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO tickets (tracking_number, subject, category_id, status_id, guest_email, description_format, source_channel)
		SELECT 'FIX-' || substr(md5(random()::text), 1, 8), 'fixture', '00000000-0000-0000-0000-0000000000aa', id, 'g@example.com', 'plain', 'web'
		FROM statuses WHERE name = 'New' RETURNING id`).Scan(&ticketID))
	return ticketID
}

func addReply(t *testing.T, ctx context.Context, db *sql.DB, ticketID, body string) (id string) {
	t.Helper()
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO ticket_replies (ticket_id, body, internal, created_at, body_format)
		VALUES ($1, $2, false, now(), 'plain') RETURNING id`, ticketID, body).Scan(&id))
	return id
}

func replyTSV(t *testing.T, ctx context.Context, db *sql.DB, id string) *string {
	t.Helper()
	var v *string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT body_tsv::text FROM ticket_replies WHERE id = $1`, id).Scan(&v))
	return v
}

// Migration 000062 adds ticket_replies.body_tsv and the trigger that keeps it
// current (reply search). It runs in an isolated database because the down and
// up files lock ticket_replies, which other packages' tests write to.
//
//   - up adds a nullable tsvector column with no default, and does it without
//     rewriting the table: a replies table of any size upgrades instantly;
//   - rows that existed before read NULL until the server fills them;
//   - the trigger fills a new reply and an edited body, with exactly the
//     expression the search queries use (to_tsvector('english', body));
//   - the server's own fill (which sets body_tsv) does not fire the trigger;
//   - down drops the column, trigger, function and both indexes, and loses no
//     reply text.
func TestMigration062_ReplySearchColumn(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()

	hasColumn := func() bool {
		var ok bool
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_name = 'ticket_replies' AND column_name = 'body_tsv' AND data_type = 'tsvector'
			  AND is_nullable = 'YES' AND column_default IS NULL)`).Scan(&ok))
		return ok
	}
	hasTrigger := func() bool {
		var ok bool
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'ticket_replies_body_tsv_trg' AND NOT tgisinternal)`).Scan(&ok))
		return ok
	}
	require.True(t, hasColumn(), "the migration has run in the test database")
	require.True(t, hasTrigger())

	t.Run("up does not rewrite the table and leaves old rows NULL", func(t *testing.T) {
		tk := repliesFixture(t, ctx, db.SQL)
		applyMigration(t, ctx, db.SQL, migration062Down)
		require.False(t, hasColumn(), "down drops the column")
		require.False(t, hasTrigger(), "and the trigger")
		old := addReply(t, ctx, db.SQL, tk, "written under 000060")

		var before, after uint32
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT relfilenode::int8 FROM pg_class WHERE oid = 'ticket_replies'::regclass`).Scan(&before))
		applyMigration(t, ctx, db.SQL, migration062Up)
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT relfilenode::int8 FROM pg_class WHERE oid = 'ticket_replies'::regclass`).Scan(&after))
		require.Equal(t, before, after, "a rewrite gives the table a new file; adding a nullable column must not")

		require.True(t, hasColumn())
		require.True(t, hasTrigger())
		require.Nil(t, replyTSV(t, ctx, db.SQL, old), "an old row is filled by the server, not by the migration")
		var body string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT body FROM ticket_replies WHERE id = $1`, old).Scan(&body))
		require.Equal(t, "written under 000060", body)
	})

	t.Run("the trigger fills an insert and an edited body, and not its own fill", func(t *testing.T) {
		tk := repliesFixture(t, ctx, db.SQL)
		id := addReply(t, ctx, db.SQL, tk, "The fuser roller is squeaking")
		got := replyTSV(t, ctx, db.SQL, id)
		require.NotNil(t, got)
		var want string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT to_tsvector('english', 'The fuser roller is squeaking')::text`).Scan(&want))
		require.Equal(t, want, *got, "the same expression the search queries match against")

		_, err := db.SQL.ExecContext(ctx, `UPDATE ticket_replies SET body = 'now about the zebra printer' WHERE id = $1`, id)
		require.NoError(t, err)
		require.Contains(t, *replyTSV(t, ctx, db.SQL, id), "zebra")
		require.NotContains(t, *replyTSV(t, ctx, db.SQL, id), "fuser")

		// The server's fill sets body_tsv without touching body: no trigger.
		_, err = db.SQL.ExecContext(ctx, `UPDATE ticket_replies SET body_tsv = NULL WHERE id = $1`, id)
		require.NoError(t, err)
		require.Nil(t, replyTSV(t, ctx, db.SQL, id))
		_, err = db.SQL.ExecContext(ctx, `UPDATE ticket_replies SET internal = true WHERE id = $1`, id)
		require.NoError(t, err)
		require.Nil(t, replyTSV(t, ctx, db.SQL, id), "an update that does not touch body leaves body_tsv alone")
	})

	t.Run("down drops the indexes too and loses no text", func(t *testing.T) {
		tk := repliesFixture(t, ctx, db.SQL)
		id := addReply(t, ctx, db.SQL, tk, "keep this text")
		for _, stmt := range []string{
			`CREATE INDEX ticket_replies_body_tsv_idx ON ticket_replies USING gin (body_tsv)`,
			`CREATE INDEX ticket_replies_created_at_idx ON ticket_replies (created_at DESC)`,
		} {
			_, err := db.SQL.ExecContext(ctx, stmt)
			require.NoError(t, err)
		}
		applyMigration(t, ctx, db.SQL, migration062Down)
		var n int
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN ('ticket_replies_body_tsv_idx', 'ticket_replies_created_at_idx')`).Scan(&n))
		require.Zero(t, n)
		var body string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT body FROM ticket_replies WHERE id = $1`, id).Scan(&body))
		require.Equal(t, "keep this text", body)
		applyMigration(t, ctx, db.SQL, migration062Up)
	})
}

// Why the index is built by the server and not by this migration: golang-migrate
// sends a migration file as one execution, and Postgres runs the several
// statements of one execution as one transaction, which CREATE INDEX
// CONCURRENTLY refuses to run in. A plain CREATE INDEX would block every new
// reply for the whole build. This runs the real runner on a throwaway migration
// set to pin the claim the upgrade notes make.
func TestMigrate_ConcurrentIndexCannotRunInAMultiStatementFile(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()

	src, err := iofs.New(fstest.MapFS{
		"1_t.up.sql": {Data: []byte("CREATE TABLE zz_concurrent (b text);")},
		"2_t.up.sql": {Data: []byte("CREATE INDEX CONCURRENTLY zz_i ON zz_concurrent (b);\nSELECT 1;")},
	}, ".")
	require.NoError(t, err)
	// A migrations table of its own: the database already carries the real one.
	m, err := migrate.NewWithSourceInstance("iofs", src,
		database.MigrateURL(db.Pool.Config().ConnString())+"&x-migrations-table=zz_migrations")
	require.NoError(t, err)
	defer m.Close()

	err = m.Up()
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot run inside a transaction block")
}
