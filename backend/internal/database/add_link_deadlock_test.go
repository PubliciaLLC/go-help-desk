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

// TestAddLink_RacesResolveAsDuplicateWithoutDeadlocking pins #210: plain
// AddLink was left outside #196's lock-ordering discipline, relying only on
// CreateLink's FK-triggered FOR KEY SHARE lock. A ResolveAsDuplicate call
// holding its ordered FOR UPDATE locks on the same ticket pair could still
// deadlock against a concurrent plain AddLink going the opposite direction:
// AddLink's FK check locks its source first, which can be the pair's
// larger id while ResolveAsDuplicate already holds FOR UPDATE on it, and
// then blocks; meanwhile ResolveAsDuplicate blocks waiting for the smaller
// id, which AddLink's FK check has already locked FOR KEY SHARE — a real
// cycle, which Postgres has to break with a raw 40P01.
//
// This runs against real Postgres, on two separate connections/transactions
// per pair, because the defect and the fix are both about row lock
// acquisition order — a fake store has no locking to get wrong. It repeats
// against many freshly-seeded pairs to make the race window likely to be hit
// at least once within the timeout, rather than relying on one lucky
// interleaving.
func TestAddLink_RacesResolveAsDuplicateWithoutDeadlocking(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	q := db.Queries
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	auStore := auditstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "addlink-deadlock-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	agentUser := user.User{
		ID: uuid.New(), Email: "addlink-deadlock-agent-" + uuid.NewString() + "@test.local",
		DisplayName: "Agent", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, agentUser))
	cat := category.Category{ID: uuid.New(), Name: "AddLinkDeadlock " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)

	mk := func(subject string) ticket.Ticket {
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("ALD-" + uuid.NewString()[:8]),
			Subject: subject, Description: subject, CategoryID: cat.ID,
			Priority: ticket.PriorityMedium, StatusID: newSt.ID, ReporterUserID: &reporter.ID,
			CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}

	t.Cleanup(func() {
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

	const iterations = 25
	var lastA, lastB ticket.Ticket
	for i := 0; i < iterations; i++ {
		a, b := mk("AddLinkDeadlock A"), mk("AddLinkDeadlock B")
		lastA, lastB = a, b
		t.Cleanup(func() {
			_, _ = db.SQL.Exec(`DELETE FROM ticket_links WHERE source_ticket_id IN ($1,$2) OR target_ticket_id IN ($1,$2)`, a.ID, b.ID)
			_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id IN ($1,$2)`, a.ID, b.ID)
		})

		type result struct{ err error }
		results := make(chan result, 2)

		// ResolveAsDuplicate(A dup-of B) and AddLink(B related_to A) —
		// opposite directions on the same pair, concurrently.
		go func() {
			_, err := svc.ResolveAsDuplicate(ctx, a.ID, b.ID, "A is a duplicate of B", staff)
			results <- result{err: err}
		}()
		go func() {
			err := svc.AddLink(ctx, b.ID, a.ID, ticket.LinkRelatedTo, staff)
			results <- result{err: err}
		}()

		for j := 0; j < 2; j++ {
			select {
			case r := <-results:
				require.NoError(t, r.err,
					"a consistent lock order must serialize AddLink and ResolveAsDuplicate, not deadlock either one (iteration %d)", i)
			case <-time.After(10 * time.Second):
				t.Fatal("both goroutines must finish; one is stuck (a real deadlock, not just contention)")
			}
		}
	}

	// Sanity: the concurrency above did real work, not just no-ops.
	resolvedA, err := ts.GetByID(ctx, lastA.ID)
	require.NoError(t, err)
	require.Equal(t, resolvedSt.ID, resolvedA.StatusID)

	links, err := ts.ListLinks(ctx, lastB.ID)
	require.NoError(t, err)
	require.NotEmpty(t, links, "AddLink must have actually written its link")
}
