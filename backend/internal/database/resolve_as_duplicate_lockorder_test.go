package database_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// barrier2 is a two-party rendezvous: the first goroutine to call arrive
// blocks until the second calls it too, then both proceed. Used below to
// force two transactions to attempt their next lock at exactly the same
// moment, rather than hoping Go's scheduler happens to interleave them badly
// on its own — TestResolveAsDuplicate_OppositeDirectionsDoNotDeadlock (in
// resolve_as_duplicate_deadlock_test.go) tries that unforced approach against
// the real Service and is a legitimate integration check, but empirically it
// did not reproduce the pre-fix deadlock reliably (two BeginTx round-trips
// rarely land close enough together). These two tests instead reproduce, at
// the SQL level, exactly the statement sequence #191... #196's before-and-
// after code issue, with the interleaving forced, so the mechanism itself —
// not luck — is what is being tested.
func newBarrier2() func() {
	ch := make(chan struct{})
	var n int32
	return func() {
		if atomic.AddInt32(&n, 1) == 2 {
			close(ch)
		} else {
			<-ch
		}
	}
}

// lockOrderFixture seeds two real, committed tickets for the raw-SQL lock
// tests below. Committed (not a rolled-back harness tx) because the two
// goroutines need separate connections that must see each other's writes.
type lockOrderFixture struct {
	a, b ticket.Ticket
}

func newLockOrderFixture(t *testing.T, db *testutil.DB) *lockOrderFixture {
	t.Helper()
	ctx := context.Background()
	us := userstore.New(db.Queries)
	cs := categorystore.New(db.Queries)
	ts := ticketstore.New(db.Queries)

	reporter := user.User{
		ID: uuid.New(), Email: "lockorder-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "LockOrder " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	mk := func(subject string) ticket.Ticket {
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("LO-" + uuid.NewString()[:8]),
			Subject: subject, Description: subject, CategoryID: cat.ID,
			Priority: ticket.PriorityMedium, StatusID: newSt.ID, ReporterUserID: &reporter.ID,
			CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}
	f := &lockOrderFixture{a: mk("LockOrder A"), b: mk("LockOrder B")}

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM ticket_links WHERE source_ticket_id IN ($1,$2) OR target_ticket_id IN ($1,$2)`, f.a.ID, f.b.ID)
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id IN ($1,$2)`, f.a.ID, f.b.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = $1`, reporter.ID)
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})
	return f
}

// TestRawLockOrder_SourceThenLinkCanDeadlock reproduces, at the SQL level,
// the exact pre-#196 hazard: ResolveAsDuplicate used to insert the
// duplicate_of link (which takes an implicit FOR KEY SHARE lock on both
// referenced ticket rows, via the FK constraint) and only afterwards take
// FOR UPDATE on the source ticket. Two opposite-direction calls each end up
// holding KEY SHARE on both rows and waiting for FOR UPDATE on the row the
// other already holds — a real lock cycle, which this test forces with a
// barrier rather than hoping for unlucky timing, and Postgres has to break
// with a 40P01 deadlock error.
//
// This is a characterization test of the hazard the fix removes, not a test
// of current production code (nothing in the repository takes locks in this
// order any more) — it exists so the mechanism behind #196 is verified
// against real Postgres locking semantics rather than asserted from reading
// the manual.
func TestRawLockOrder_SourceThenLinkCanDeadlock(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	f := newLockOrderFixture(t, db)

	arrive := newBarrier2()
	type outcome struct{ err error }
	results := make(chan outcome, 2)

	run := func(sourceID, targetID uuid.UUID) {
		tx, err := db.SQL.Begin()
		if err != nil {
			results <- outcome{err}
			return
		}
		defer func() { _ = tx.Rollback() }()

		// The old order: the link insert (and its implicit FK lock on both
		// rows) happens first.
		if _, err := tx.Exec(
			`INSERT INTO ticket_links (source_ticket_id, target_ticket_id, link_type) VALUES ($1,$2,'duplicate_of')`,
			sourceID, targetID,
		); err != nil {
			results <- outcome{err}
			return
		}

		// Wait for the other transaction to also have taken its KEY SHARE
		// locks before either proceeds to FOR UPDATE — this is the forcing
		// step; without it the two BeginTx calls above might not land close
		// enough in time for either to hold both KEY SHARE locks when the
		// other reaches FOR UPDATE.
		arrive()

		var discard uuid.UUID
		err = tx.QueryRow(`SELECT id FROM tickets WHERE id = $1 FOR UPDATE`, sourceID).Scan(&discard)
		if err != nil {
			results <- outcome{err}
			return
		}
		results <- outcome{tx.Commit()}
	}

	go run(f.a.ID, f.b.ID)
	go run(f.b.ID, f.a.ID)

	var outcomes []outcome
	for i := 0; i < 2; i++ {
		select {
		case o := <-results:
			outcomes = append(outcomes, o)
		case <-time.After(10 * time.Second):
			t.Fatal("both goroutines must finish (Postgres's own deadlock_timeout bounds this)")
		}
	}

	var deadlocks, successes int
	for _, o := range outcomes {
		if o.err == nil {
			successes++
			continue
		}
		var pgErr *pgconn.PgError
		require.True(t, errors.As(o.err, &pgErr), "expected a Postgres error, got: %v", o.err)
		require.Equal(t, "40P01", pgErr.Code, "expected a deadlock error, got: %v", o.err)
		deadlocks++
	}
	require.Equal(t, 1, deadlocks, "this lock order must produce exactly one deadlock victim")
	require.Equal(t, 1, successes, "the survivor must complete cleanly")
}

// TestRawLockOrder_BothLocksBeforeLinkAvoidsDeadlock is
// TestRawLockOrder_SourceThenLinkCanDeadlock's mirror: the same two
// tickets, the same opposite-direction pairing, but with #196's fix — both
// ticket rows locked FOR UPDATE, in a fixed order, before the link insert.
// No barrier is used here: with a consistent lock order the two
// transactions' very first statements already contend for the same row, so
// there is no interleaving left that could cycle (the second transaction
// simply cannot get far enough ahead to create one) — which is the point.
// Both must complete without error.
func TestRawLockOrder_BothLocksBeforeLinkAvoidsDeadlock(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	f := newLockOrderFixture(t, db)

	results := make(chan error, 2)

	run := func(sourceID, targetID uuid.UUID) {
		tx, err := db.SQL.Begin()
		if err != nil {
			results <- err
			return
		}
		defer func() { _ = tx.Rollback() }()

		// Same comparison production code uses (ResolveAsDuplicate in
		// internal/domain/ticket/service.go), so this test mirrors the fix
		// exactly rather than merely some consistent order.
		first, second := sourceID, targetID
		if bytes.Compare(second[:], first[:]) < 0 {
			first, second = second, first
		}
		var discard uuid.UUID
		if err := tx.QueryRow(`SELECT id FROM tickets WHERE id = $1 FOR UPDATE`, first).Scan(&discard); err != nil {
			results <- err
			return
		}
		if err := tx.QueryRow(`SELECT id FROM tickets WHERE id = $1 FOR UPDATE`, second).Scan(&discard); err != nil {
			results <- err
			return
		}
		if _, err := tx.Exec(
			`INSERT INTO ticket_links (source_ticket_id, target_ticket_id, link_type) VALUES ($1,$2,'duplicate_of')`,
			sourceID, targetID,
		); err != nil {
			results <- err
			return
		}
		results <- tx.Commit()
	}

	go run(f.a.ID, f.b.ID)
	go run(f.b.ID, f.a.ID)

	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			require.NoError(t, err, "a consistent lock order must never deadlock, regardless of call direction")
		case <-time.After(10 * time.Second):
			t.Fatal("both goroutines must finish; a hang here would itself be the bug")
		}
	}
}
