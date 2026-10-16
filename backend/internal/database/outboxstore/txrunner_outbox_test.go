package outboxstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
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
	db, closeDB := testutil.NewDB(t)
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)
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
	db, closeDB := testutil.NewDB(t)
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)
	defer closeDB()
	woken := 0
	r := txrunner.New(db.SQL, allChannels, func() { woken++ })
	tid := uuid.New()

	err = r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
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
	db, closeDB := testutil.NewDB(t)
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)
	defer closeDB()
	r := txrunner.New(db.SQL, []string{"email"}, nil)
	tid := uuid.New()

	err = r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
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
	db, closeDB := testutil.NewDB(t)
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)
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
// own: the rows are one set, not two.
func TestInTx_ADeadlockRetryLeavesOneSetOfRows(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	_, err := db.SQL.Exec(`DELETE FROM notification_outbox`)
	require.NoError(t, err)
	defer closeDB()
	r := txrunner.New(db.SQL, allChannels, nil)
	tid := uuid.New()

	attempts := 0
	require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store, ch *notification.Changes) error {
		attempts++
		require.NoError(t, au.Create(context.Background(), entry(tid, "attempt")))
		raise(ch, tid, notification.EventTicketStatusChanged)
		if attempts == 1 {
			return &pgconn.PgError{Code: "40P01"} // what Postgres says to the transaction it kills
		}
		return nil
	}))

	require.Equal(t, 2, attempts)
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, outboxRows(t, db, tid))
	require.Equal(t, 1, visible(t, db, tid))
}
