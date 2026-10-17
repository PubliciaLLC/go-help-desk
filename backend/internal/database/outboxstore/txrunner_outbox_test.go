package outboxstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The transaction runner writes the outbox rows (#506). These tests commit for
// real against the shared outbox queue, so they live beside the outbox store's
// own tests, which already own that table (each clears it) and run one after
// another; in a package of their own they would race those.

var errCallerFailed = errors.New("the caller failed after writing")

func entry(entityID uuid.UUID, action string) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), EntityType: "txrunner-test", EntityID: entityID, Action: action, CreatedAt: time.Now(),
	}
}

// visible counts committed audit rows for an entity.
func visible(t *testing.T, db *testutil.DB, entityID uuid.UUID) int {
	t.Helper()
	entries, err := auditstore.New(db.Queries).ListByEntity(context.Background(), "txrunner-test", entityID, 100, 0)
	require.NoError(t, err)
	return len(entries)
}

// outboxRows counts the queued rows for a ticket per channel, read outside any
// transaction the test holds. The ticket id is fresh per test.
func outboxRows(t *testing.T, db *testutil.DB, ticketID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := db.SQL.QueryContext(context.Background(),
		`SELECT channel, count(*) FROM notification_outbox WHERE event->>'ticket_id' = $1 GROUP BY channel`,
		ticketID.String())
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ch string
		var n int
		require.NoError(t, rows.Scan(&ch, &n))
		out[ch] = n
	}
	require.NoError(t, rows.Err())
	return out
}

func raise(ch *notification.Changes, ticketID uuid.UUID, kinds ...notification.EventType) {
	ch.Raise(notification.Event{
		Kinds: kinds, TicketID: ticketID, OccurredAt: time.Now(),
		Recipient: "guest@example.test", // gives the email channel something to carry
		Ticket:    &notification.TicketState{Priority: "low"},
	})
}

var allChannels = []string{"email", "webhook", "verification"}

// The change and its announcement are one commit: the rows are there the
// moment InTx returns, before any worker has run.
func TestInTx_WritesOneRowPerCarryingChannelWithTheChange(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	woken := 0
	r := txrunner.New(db.SQL, allChannels, func() { woken++ })
	tid := uuid.New()

	require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
		require.NoError(t, au.Create(context.Background(), entry(tid, "committed")))
		raise(ch, tid, notification.EventTicketStatusChanged)
		raise(ch, tid, notification.EventTicketAssigned) // merged: still one event
		return nil
	}))

	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, outboxRows(t, db, tid),
		"one row for each channel that carries the event; verification does not")
	require.Equal(t, 1, woken, "the worker is woken after the commit")
}

// The point of the change: a rolled-back change leaves no row. Before #506 the
// rows were written after the commit, so this could not be asked of them.
func TestInTx_ARolledBackChangeLeavesNoOutboxRow(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	woken := 0
	r := txrunner.New(db.SQL, allChannels, func() { woken++ })
	tid := uuid.New()

	err := r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
		require.NoError(t, au.Create(context.Background(), entry(tid, "rolled back")))
		raise(ch, tid, notification.EventTicketStatusChanged)
		return errCallerFailed
	})
	require.ErrorIs(t, err, errCallerFailed)

	require.Empty(t, outboxRows(t, db, tid), "a rolled-back change announces nothing")
	require.Equal(t, 0, visible(t, db, tid))
	require.Zero(t, woken)
}

// A failure writing the rows fails the change: the announcement is part of it.
func TestInTx_AnOutboxWriteFailureRollsBackTheChange(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	r := txrunner.New(db.SQL, []string{"email"}, nil)
	tid := uuid.New()

	err := r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
		require.NoError(t, au.Create(context.Background(), entry(tid, "must not stay")))
		// A raw guest token is refused by the encoder; the change goes with it.
		ch.Raise(notification.Event{Kinds: []notification.EventType{notification.EventTicketReplied},
			TicketID: tid, Recipient: "g@example.test", GuestToken: "raw"})
		return nil
	})
	require.ErrorIs(t, err, notification.ErrRawGuestToken)
	require.Equal(t, 0, visible(t, db, tid))
}

// A change that raised nothing writes nothing and wakes nobody.
func TestInTx_NothingRaisedWritesNothing(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	woken := 0
	r := txrunner.New(db.SQL, allChannels, func() { woken++ })
	tid := uuid.New()

	require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, _ *notification.Changes) error {
		return au.Create(context.Background(), entry(tid, "quiet"))
	}))
	require.Empty(t, outboxRows(t, db, tid))
	require.Zero(t, woken)
}

// The transaction is retried once after a deadlock, with a collector of its
// own: the attempt that was rolled back announced ticket A, the one that
// committed announced ticket B, and only B has rows. A collector shared between
// the attempts would write A's event as well.
func TestInTx_ADeadlockRetryAnnouncesOnlyTheAttemptThatCommitted(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	r := txrunner.New(db.SQL, allChannels, nil)
	a, b := uuid.New(), uuid.New()

	attempts := 0
	require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
		attempts++
		if attempts == 1 {
			raise(ch, a, notification.EventTicketStatusChanged)
			return &pgconn.PgError{Code: "40P01"} // what Postgres says to the transaction it kills
		}
		require.NoError(t, au.Create(context.Background(), entry(b, "attempt 2")))
		raise(ch, b, notification.EventTicketAssigned)
		return nil
	}))

	require.Equal(t, 2, attempts)
	require.Empty(t, outboxRows(t, db, a), "the rolled-back attempt announced nothing")
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, outboxRows(t, db, b))
	require.Equal(t, 1, visible(t, db, b), "and the audit trail committed with the rows")
}

// Split queues a change's rows and deletes the change's own row in one
// transaction: a row that cannot be inserted leaves no sibling behind and the
// change's row where it was, to be tried again (F1/#506).
func TestStore_SplitIsAllOrNothing(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := outboxstore.NewWithDB(db.SQL)
	parent := uuid.New()
	require.NoError(t, s.Enqueue(ctx, parent, "email", []byte(`{"type":"ticket.created"}`)))

	err := s.Split(ctx, parent, []notification.NewRow{
		{ID: uuid.New(), Channel: "email", Event: []byte(`{"type":"staff.assigned"}`)},
		{ID: uuid.New(), Channel: "email", Event: []byte(`not json`)}, // jsonb refuses it
	})
	require.Error(t, err)
	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox`).Scan(&n))
	require.Equal(t, 1, n, "no child was queued, and the change's row is still there")

	good := []notification.NewRow{
		{ID: uuid.New(), Channel: "email", Event: []byte(`{"type":"staff.assigned"}`)},
		{ID: uuid.New(), Channel: "email", Event: []byte(`{"type":"ticket.created"}`)},
	}
	require.NoError(t, s.Split(ctx, parent, good))
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox WHERE id = $1`, parent).Scan(&n))
	require.Zero(t, n, "the change's row is settled with them")
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox`).Scan(&n))
	require.Equal(t, 2, n)
}

// A split that outlasts the claim lease lets a second worker claim and split
// the same parent. The first split's children may already be sent and deleted
// by then, so the second one's ON CONFLICT has nothing to collide with and
// would queue them again: the customer gets the mail twice (#513). The parent
// is the lock: whoever cannot delete it was too late and queues nothing.
func TestStore_SplitOfASettledParentQueuesNothing(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := outboxstore.NewWithDB(db.SQL)
	parent := uuid.New()
	require.NoError(t, s.Enqueue(ctx, parent, "email", []byte(`{"type":"ticket.created"}`)))
	children := []notification.NewRow{
		{ID: uuid.NewSHA1(parent, []byte("a")), Channel: "email", Event: []byte(`{"type":"staff.assigned"}`)},
		{ID: uuid.NewSHA1(parent, []byte("b")), Channel: "email", Event: []byte(`{"type":"ticket.created"}`)},
	}

	require.NoError(t, s.Split(ctx, parent, children))
	// The children are sent and settled before the slow first split's rival runs.
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)

	require.NoError(t, s.Split(ctx, parent, children), "losing the race is not an error")
	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox`).Scan(&n))
	require.Zero(t, n, "the second split queued the children again")
}

// The same race at the same instant: only one of several concurrent splits of
// one parent queues anything. Each caller brings its own child ids, so the
// ON CONFLICT cannot hide a second split.
func TestStore_ConcurrentSplitsOfOneParentQueueOnce(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	s := outboxstore.NewWithDB(db.SQL)
	parent := uuid.New()
	require.NoError(t, s.Enqueue(ctx, parent, "email", []byte(`{"type":"ticket.created"}`)))

	const workers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.Split(ctx, parent, []notification.NewRow{
				{ID: uuid.New(), Channel: "email", Event: []byte(`{"type":"staff.assigned"}`)},
				{ID: uuid.New(), Channel: "email", Event: []byte(`{"type":"ticket.created"}`)},
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()

	var n int
	require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM notification_outbox`).Scan(&n))
	require.Equal(t, 2, n, "one split's children, not %d splits' worth", workers)
}
