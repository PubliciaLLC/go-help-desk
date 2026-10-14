package outboxstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Run against a real connection pool rather than the usual rolled-back
// transaction: claiming is about concurrent statements, and one transaction
// serialises them by construction. Each test deletes the rows it made.

func newStore(t *testing.T) (*outboxstore.Store, *testutil.DB, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	clean := func() {
		_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
		require.NoError(t, err)
	}
	clean()
	return outboxstore.New(dbgen.New(db.SQL)), db, func() { clean(); closeDB() }
}

func enqueue(t *testing.T, s *outboxstore.Store, n int) map[uuid.UUID]bool {
	t.Helper()
	ids := map[uuid.UUID]bool{}
	for range n {
		id := uuid.New()
		require.NoError(t, s.Enqueue(context.Background(), id, "email", []byte(`{"type":"ticket.created"}`)))
		ids[id] = true
	}
	return ids
}

// Several workers claiming at once — several replicas, or one replica's
// worker overlapping itself — must never be handed the same row, or a
// customer gets the same email twice.
func TestClaim_ConcurrentWorkersNeverShareARow(t *testing.T) {
	s, _, done := newStore(t)
	defer done()
	want := enqueue(t, s, 200)

	var (
		mu    sync.Mutex
		seen  = map[uuid.UUID]int{}
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Bounded: 200 rows in batches of 7 empty well inside this, and
			// a claim that hands back leased rows (the lease check broken)
			// would otherwise loop until the test timeout instead of failing.
			for range 100 {
				rows, err := s.Claim(context.Background(), 7, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, r := range rows {
					seen[r.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Len(t, seen, len(want), "some rows were never claimed")
	for id, n := range seen {
		require.True(t, want[id])
		require.Equal(t, 1, n, "row %s was claimed by %d workers", id, n)
	}
}

// A claimed row belongs to its worker until the lease runs out, and then is
// due again — the case of a worker that died mid-send. Each claim counts as
// an attempt, so a row that kills its worker every time still runs out.
func TestClaim_LeaseHoldsThenExpires(t *testing.T) {
	s, _, done := newStore(t)
	defer done()
	enqueue(t, s, 1)

	rows, err := s.Claim(context.Background(), 10, time.Second)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].Attempts)

	again, err := s.Claim(context.Background(), 10, time.Second)
	require.NoError(t, err)
	require.Empty(t, again, "a leased row was handed out again")

	time.Sleep(1100 * time.Millisecond)
	again, err = s.Claim(context.Background(), 10, time.Second)
	require.NoError(t, err)
	require.Len(t, again, 1, "a row whose lease ran out was not reclaimed")
	require.Equal(t, 2, again[0].Attempts)
}

// Rescheduled rows wait for their time; failed rows are never claimed; a
// delivered row is gone; old failed rows are cleaned up.
func TestRetryFailDelete(t *testing.T) {
	s, db, done := newStore(t)
	defer done()
	ctx := context.Background()
	enqueue(t, s, 3)

	rows, err := s.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	later, failed, delivered := rows[0].ID, rows[1].ID, rows[2].ID

	require.NoError(t, s.Retry(ctx, later, time.Now().Add(time.Hour), "smtp down"))
	require.NoError(t, s.Fail(ctx, failed, "gave up"))
	require.NoError(t, s.Delete(ctx, delivered))

	again, err := s.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, again, "a rescheduled or failed row was claimed early")

	require.NoError(t, s.Retry(ctx, later, time.Now().Add(-time.Second), "smtp down"))
	again, err = s.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.Equal(t, later, again[0].ID)

	var remaining int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox`).Scan(&remaining))
	require.Equal(t, 2, remaining, "a delivered row was not deleted")

	n, err := s.DeleteFailedBefore(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Zero(t, n, "a recently failed row was cleaned up too soon")
	n, err = s.DeleteFailedBefore(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

// A failed webhook row is scrubbed in place: the event changes, the row, its
// state and its error do not.
func TestRewriteEvent_ReplacesOnlyTheEvent(t *testing.T) {
	s, db, done := newStore(t)
	defer done()
	ctx := context.Background()
	id, other := uuid.New(), uuid.New()
	require.NoError(t, s.Enqueue(ctx, id, "webhook", []byte(`{"subject":"SECRET"}`)))
	require.NoError(t, s.Enqueue(ctx, other, "webhook", []byte(`{"subject":"KEEP"}`)))
	require.NoError(t, s.Fail(ctx, id, "gave up"))

	require.NoError(t, s.RewriteEvent(ctx, id, []byte(`{"type":"ticket.replied"}`)))

	var ev, lastErr string
	var failed bool
	require.NoError(t, db.SQL.QueryRow(
		`SELECT event::text, last_error, failed_at IS NOT NULL FROM notification_outbox WHERE id = $1`, id).Scan(&ev, &lastErr, &failed))
	require.JSONEq(t, `{"type":"ticket.replied"}`, ev)
	require.Equal(t, "gave up", lastErr)
	require.True(t, failed)

	require.NoError(t, db.SQL.QueryRow(`SELECT event::text FROM notification_outbox WHERE id = $1`, other).Scan(&ev))
	require.JSONEq(t, `{"subject":"KEEP"}`, ev, "another row was touched")
}

// The fan-out relies on a repeated id being ignored.
func TestEnqueue_ARepeatedIDIsIgnored(t *testing.T) {
	s, db, done := newStore(t)
	defer done()
	ctx := context.Background()
	id := uuid.New()
	require.NoError(t, s.Enqueue(ctx, id, "webhook", []byte(`{"n":1}`)))
	require.NoError(t, s.Enqueue(ctx, id, "webhook", []byte(`{"n":2}`)))

	var n int
	var ev string
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*), min(event::text) FROM notification_outbox WHERE id = $1`, id).Scan(&n, &ev))
	require.Equal(t, 1, n)
	require.JSONEq(t, `{"n":1}`, ev, "the first row stands")
}
