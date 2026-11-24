package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Every lifecycle write used to read the ticket on the pool, mutate the whole
// struct, and UPDATE all of it inside a transaction. UpdateTicket overwrites
// every column, so two staff acting within a few milliseconds silently lost one
// change — and the history and audit rows for the lost change still committed,
// so the ticket contradicted its own timeline.
//
// This runs against real Postgres on separate connections, because the defect
// and the fix are both about row locking. Against a map it would prove nothing.
func TestTicketStore_ConcurrentWritesDoNotLoseEachOther(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	// Registered before the row cleanup below so it runs AFTER it: t.Cleanup is
	// LIFO, and `defer closeDB()` would run before either, leaving the deletes
	// to talk to a closed pool. These rows are committed — the two connections
	// need to see each other — so a leak here fails the tests that assert an
	// empty table.
	t.Cleanup(closeDB)
	ctx := context.Background()

	// Committed rows: the two writers need separate connections, so a rolled
	// back harness transaction would hide the row from both.
	q := db.Queries
	us, cs, ts := userstore.New(q), categorystore.New(q), ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "concurrency-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Conc " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	// A real user to assign to: assignee_user_id has a foreign key.
	agent := user.User{
		ID: uuid.New(), Email: "agent-" + uuid.NewString() + "@test.local",
		DisplayName: "Agent", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, agent))

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE reporter_user_id = $1`, reporter.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = ANY($1)`,
			[]uuid.UUID{reporter.ID, agent.ID})
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("CONC-" + uuid.NewString()[:8]),
		Subject: "Contended", Description: "two writers", CategoryID: cat.ID,
		Priority: ticket.PriorityHigh, StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))

	// The property, stated directly: a second writer's read must not return
	// until the first writer commits, and must then see what the first wrote.
	//
	// Racing two goroutines and hoping for an unlucky interleaving does not
	// work — the writes are tiny and serialise naturally, so such a test
	// passes with or without the lock. Verified: removing FOR UPDATE leaves a
	// race-style version of this test green.
	assignee := agent.ID

	tx1, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback() }()
	store1 := ticketstore.New(dbgen.New(tx1))

	locked, err := store1.GetByIDForUpdate(ctx, tk.ID)
	require.NoError(t, err)
	locked.AssigneeUserID = &assignee
	require.NoError(t, store1.Update(ctx, locked))
	// tx1 holds the row, uncommitted.

	secondRead := make(chan ticket.Ticket, 1)
	readErr := make(chan error, 1)
	go func() {
		tx2, err := db.SQL.Begin()
		if err != nil {
			readErr <- err
			return
		}
		defer func() { _ = tx2.Rollback() }()
		got, err := ticketstore.New(dbgen.New(tx2)).GetByIDForUpdate(ctx, tk.ID)
		if err != nil {
			readErr <- err
			return
		}
		secondRead <- got
	}()

	// It must still be waiting: the lock is what makes the second writer
	// compute from the first's result instead of overwriting it.
	select {
	case <-secondRead:
		t.Fatal("the second locking read returned while the first transaction " +
			"still held the row — without that wait, the second writer computes " +
			"from a stale copy and erases the first writer's change")
	case err := <-readErr:
		t.Fatalf("second read failed: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, tx1.Commit())

	select {
	case got := <-secondRead:
		require.NotNil(t, got.AssigneeUserID,
			"the second writer must see the first writer's committed assignment")
		require.Equal(t, assignee, *got.AssigneeUserID)
	case err := <-readErr:
		t.Fatalf("second read failed after commit: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the second read never returned after the first committed")
	}
}
