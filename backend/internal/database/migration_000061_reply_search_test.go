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
	migration061Up   = "migrations/000061_reply_search.up.sql"
	migration061Down = "migrations/000061_reply_search.down.sql"

	replyFTSIndex = "ticket_replies_body_fts_idx"

	// The statement the upgrade notes tell an operator with a large reply table
	// to run on 1.2.x. A test builds the index with exactly this text, so the
	// advice cannot drift from what 000061 expects to find.
	documentedPrebuild = `CREATE INDEX CONCURRENTLY IF NOT EXISTS ticket_replies_body_fts_idx
		ON ticket_replies USING gin (to_tsvector('english', body))`
)

type replyIndexState struct {
	exists bool
	valid  bool
	oid    uint32
	def    string
}

func replyIndex(t *testing.T, ctx context.Context, db *sql.DB) replyIndexState {
	t.Helper()
	var st replyIndexState
	err := db.QueryRowContext(ctx, `SELECT c.oid::int8, i.indisvalid, pg_get_indexdef(c.oid)
		FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
		WHERE c.oid = to_regclass($1)`, replyFTSIndex).Scan(&st.oid, &st.valid, &st.def)
	if err == sql.ErrNoRows {
		return replyIndexState{}
	}
	require.NoError(t, err)
	st.exists = true
	return st
}

// Migration 000061 indexes reply text for the ticket search. It runs in an
// isolated database because DROP INDEX and CREATE INDEX lock ticket_replies,
// which the other packages' tests write to while this one holds its locks.
//
//   - up builds a GIN expression index on to_tsvector('english', body), which
//     is the exact expression the search queries use: a different spelling and
//     the planner will not use the index;
//   - down drops it and loses no data;
//   - an index an operator built beforehand (the documented CREATE INDEX
//     CONCURRENTLY) is left alone, so the upgrade does not rebuild it;
//   - a build that failed partway leaves an INVALID index under that name,
//     which IF NOT EXISTS would skip without a word, so up replaces it.
func TestMigration061_ReplyIndex(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()

	t.Run("up, down, up", func(t *testing.T) {
		st := replyIndex(t, ctx, db.SQL)
		require.True(t, st.exists, "the migration has run in the test database")
		require.True(t, st.valid)
		require.Contains(t, st.def, "USING gin")
		require.Contains(t, st.def, "to_tsvector('english'::regconfig, body)")

		applyMigration(t, ctx, db.SQL, migration061Down)
		require.False(t, replyIndex(t, ctx, db.SQL).exists, "down drops the index")

		applyMigration(t, ctx, db.SQL, migration061Up)
		st = replyIndex(t, ctx, db.SQL)
		require.True(t, st.exists && st.valid, "up builds it again")
	})

	t.Run("down loses no data", func(t *testing.T) {
		var before, after int
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM ticket_replies`).Scan(&before))
		applyMigration(t, ctx, db.SQL, migration061Down)
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM ticket_replies`).Scan(&after))
		require.Equal(t, before, after)
		applyMigration(t, ctx, db.SQL, migration061Up)
	})

	t.Run("a prebuilt valid index is left alone", func(t *testing.T) {
		applyMigration(t, ctx, db.SQL, migration061Down)
		// Autocommit, outside a transaction: CREATE INDEX CONCURRENTLY refuses
		// to run inside one.
		_, err := db.SQL.ExecContext(ctx, documentedPrebuild)
		require.NoError(t, err)
		built := replyIndex(t, ctx, db.SQL)
		require.True(t, built.exists && built.valid)

		applyMigration(t, ctx, db.SQL, migration061Up)
		after := replyIndex(t, ctx, db.SQL)
		require.Equal(t, built.oid, after.oid, "the same index, not rebuilt")
		require.Equal(t, built.def, after.def)
	})

	t.Run("an invalid index is replaced", func(t *testing.T) {
		before := replyIndex(t, ctx, db.SQL)
		require.True(t, before.valid)
		// What a failed CREATE INDEX CONCURRENTLY leaves behind.
		_, err := db.SQL.ExecContext(ctx, `UPDATE pg_index SET indisvalid = false WHERE indexrelid = to_regclass($1)`, replyFTSIndex)
		require.NoError(t, err)
		require.False(t, replyIndex(t, ctx, db.SQL).valid)

		applyMigration(t, ctx, db.SQL, migration061Up)
		after := replyIndex(t, ctx, db.SQL)
		require.True(t, after.exists)
		require.True(t, after.valid, "up dropped the broken index and built a good one")
		require.NotEqual(t, before.oid, after.oid, "a new index, not the old one revalidated")
	})

	t.Run("up is a no-op when run twice", func(t *testing.T) {
		before := replyIndex(t, ctx, db.SQL)
		applyMigration(t, ctx, db.SQL, migration061Up)
		require.Equal(t, before, replyIndex(t, ctx, db.SQL))
	})
}

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

// Why 000061 builds the index with a plain CREATE INDEX and not CONCURRENTLY:
// golang-migrate sends a migration file as one execution, and Postgres runs the
// several statements of one execution as one transaction, which CREATE INDEX
// CONCURRENTLY refuses to run in. (The server also migrates before it takes
// requests, so a concurrent build would only be slower here: it reads the table
// twice.) This runs the real runner on a throwaway migration set to pin the
// claim the upgrade notes make.
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
