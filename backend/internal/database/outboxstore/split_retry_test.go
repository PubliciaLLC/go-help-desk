package outboxstore_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Split is one transaction, and it can be aborted to break a lock cycle like
// any other (#508): it settles the parent (a row lock) and then inserts the
// children, so a transaction that holds a child's key and wants the parent
// closes a cycle with it. The split is run again, from the start; nothing in
// it is kept between attempts.
//
// Staged. A partner transaction has inserted the child, uncommitted, so the
// split's insert of it waits. Once the split is visibly waiting, the partner
// reaches for the parent, which the split holds: a cycle. The partner's
// deadlock timer is an hour, the split's three seconds (a connection of its
// own with deadlock_timeout set on its database), so the split is the victim
// and has seconds, not one, for the partner to arrive.
func TestStore_SplitRunsAgainAfterADeadlock(t *testing.T) {
	const testLimit = 60 * time.Second
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	ctx, cancel := context.WithTimeout(context.Background(), testLimit)
	defer cancel()

	var dbName string
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbName))
	_, err := db.SQL.ExecContext(ctx, `ALTER DATABASE `+dbName+` SET deadlock_timeout = '3s'`)
	require.NoError(t, err, "setting deadlock_timeout needs a superuser role")
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	require.NoError(t, err)
	u.Path = "/" + dbName
	splitDB, err := sql.Open("pgx", u.String()) // opened after the ALTER, so it has the 3 s
	require.NoError(t, err)
	t.Cleanup(func() { _ = splitDB.Close() })

	s := outboxstore.NewWithDB(splitDB)
	parent, child := uuid.New(), uuid.New()
	event := []byte(`{"type":"staff.assigned"}`)
	require.NoError(t, outboxstore.New(db.Queries).Enqueue(ctx, parent, "email", event))

	partner, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = partner.Rollback() })
	_, err = partner.ExecContext(ctx, `SET LOCAL deadlock_timeout = '1h'`)
	require.NoError(t, err)
	_, err = partner.ExecContext(ctx, `INSERT INTO notification_outbox (id, channel, event) VALUES ($1, 'email', $2)`, child, event)
	require.NoError(t, err)

	split := make(chan error, 1)
	go func() {
		split <- s.Split(ctx, parent, []notification.NewRow{{ID: child, Channel: "email", Event: event}})
	}()

	require.Eventually(t, func() bool {
		var n int
		require.NoError(t, db.SQL.QueryRowContext(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%INSERT INTO notification_outbox%' AND pid <> pg_backend_pid()`).Scan(&n))
		return n == 1
	}, testLimit, 5*time.Millisecond, "the split never waited on the child")

	// Closes the cycle; returns once Postgres has killed the split. The partner
	// then lets go of everything so the retry can run.
	_, err = partner.ExecContext(ctx, `DELETE FROM notification_outbox WHERE id = $1`, parent)
	require.NoError(t, err, "the partner was killed instead of the split")
	require.NoError(t, partner.Rollback())

	select {
	case err := <-split:
		require.NoError(t, err, "the split was killed to break a lock cycle and reported as a failure")
	case <-ctx.Done():
		t.Fatal("the split never finished")
	}

	var parents, children int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox WHERE id = $1`, parent).Scan(&parents))
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox WHERE id = $1`, child).Scan(&children))
	require.Zero(t, parents, "the parent is settled")
	require.Equal(t, 1, children, "the child is queued once")
}
