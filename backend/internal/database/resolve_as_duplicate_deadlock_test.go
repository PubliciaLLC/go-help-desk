package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TestResolveAsDuplicate_OppositeDirectionsDoNotDeadlock pins #196: two
// concurrent ResolveAsDuplicate calls resolving the same ticket pair in
// opposite directions (A duplicate-of B, and B duplicate-of A) must not
// deadlock.
//
// Before the fix, CreateLink's FK check takes a FOR KEY SHARE lock on both
// referenced ticket rows, and resolveInTx's GetByIDForUpdate later takes FOR
// UPDATE on the source. Call 1 (A dup-of B) ends up holding KEY SHARE on
// {A,B} and waiting for FOR UPDATE on A, while call 2 (B dup-of A) holds KEY
// SHARE on {B,A} and waits for FOR UPDATE on B — a lock cycle Postgres has to
// abort with a raw 40P01 deadlock error on whichever transaction it picks as
// the victim.
//
// This runs against real Postgres, on two separate connections/transactions,
// because the defect and the fix are both about row lock acquisition order —
// a fake store has no locking to get wrong.
func TestResolveAsDuplicate_OppositeDirectionsDoNotDeadlock(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	q := db.Queries
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	auStore := auditstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "deadlock-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	agentUser := user.User{
		ID: uuid.New(), Email: "deadlock-agent-" + uuid.NewString() + "@test.local",
		DisplayName: "Agent", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, agentUser))
	cat := category.Category{ID: uuid.New(), Name: "Deadlock " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	mk := func(subject string) ticket.Ticket {
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("DL-" + uuid.NewString()[:8]),
			Subject: subject, Description: subject, CategoryID: cat.ID,
			Priority: ticket.PriorityMedium, StatusID: newSt.ID, ReporterUserID: &reporter.ID,
			CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}
	a := mk("Deadlock A")
	b := mk("Deadlock B")

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM ticket_links WHERE source_ticket_id IN ($1,$2) OR target_ticket_id IN ($1,$2)`, a.ID, b.ID)
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id IN ($1,$2)`, a.ID, b.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{reporter.ID, agentUser.ID})
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})

	// A real Runner (its own BeginTx per call), not the harness's
	// single-shared-transaction JoiningTxRunner: the two calls need genuinely
	// separate transactions on separate connections for a deadlock to be
	// possible at all.
	svc := ticket.NewService(ts, ts, notification.Noop{}, auStore, txrunner.New(db.SQL), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))

	staff := ticket.Actor{UserID: &agentUser.ID, Role: user.RoleStaff}

	type result struct {
		err error
	}
	results := make(chan result, 2)

	run := func(source, target uuid.UUID, notes string) {
		_, err := svc.ResolveAsDuplicate(ctx, source, target, notes, staff)
		results <- result{err: err}
	}

	go run(a.ID, b.ID, "A is a duplicate of B")
	go run(b.ID, a.ID, "B is a duplicate of A")

	var errs []error
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			errs = append(errs, r.err)
		case <-time.After(10 * time.Second):
			t.Fatal("both goroutines must finish; one is stuck (a real deadlock, not just contention)")
		}
	}

	// Neither call has any real business conflict with the other — they
	// touch different link rows (A->B and B->A) and resolve different
	// tickets — so with locks acquired in a consistent order both must simply
	// serialize and both succeed. Neither may surface a raw driver error.
	for _, err := range errs {
		require.NoError(t, err, "a consistent lock order must serialize these calls, not deadlock either one")
	}

	resolvedA, err := ts.GetByID(ctx, a.ID)
	require.NoError(t, err)
	resolvedB, err := ts.GetByID(ctx, b.ID)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)
	require.Equal(t, resolvedSt.ID, resolvedA.StatusID)
	require.Equal(t, resolvedSt.ID, resolvedB.StatusID)

	// ListLinks returns links in either direction touching the given ticket,
	// so both a.ID and b.ID see both rows; check them as an unordered set.
	links, err := ts.ListLinks(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, links, 2, "both directed duplicate_of links must exist, not just one")
	seen := map[[2]uuid.UUID]bool{}
	for _, l := range links {
		require.Equal(t, ticket.LinkDuplicateOf, l.LinkType)
		seen[[2]uuid.UUID{l.SourceTicketID, l.TargetTicketID}] = true
	}
	require.True(t, seen[[2]uuid.UUID{a.ID, b.ID}], "A duplicate-of B must exist")
	require.True(t, seen[[2]uuid.UUID{b.ID, a.ID}], "B duplicate-of A must exist")
}
