package database_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #164 round 1 (HIGH): since guest links are created at send time, two
// workers sending for one ticket at once — two replicas, or a reclaimed row
// beside a fresh one — each rotated with a DELETE and an INSERT on autocommit.
// Interleaved as DELETE, DELETE, INSERT, INSERT they left two working links,
// and "rotation replaces rather than accumulates" held only when sends came
// one after another. The same bug a comment in AddReply records fixing once.
//
// Real Postgres on separate connections: the defect and the fix are both
// about row locking, and a single transaction would serialise them.
func TestIssueGuestLink_ConcurrentSendsLeaveOneWorkingLink(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	q := db.Queries
	cs, ts := categorystore.New(q), ticketstore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "Guest race " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	email := "race-" + uuid.NewString()[:8] + "@test.local"
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("GRACE-" + uuid.NewString()[:8]),
		Subject: "Racing sends", Description: "two workers", CategoryID: cat.ID,
		Priority: ticket.PriorityLow, StatusID: newSt.ID, GuestEmail: &email, GuestName: "Ada",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))
	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, tk.ID)
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})

	svc := ticket.NewService(ts, ts, notification.Noop{}, auditstore.New(q), txrunner.New(db.SQL), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))

	ev := notification.Event{Type: notification.EventTicketReplied, TicketID: tk.ID, GuestLink: true}
	for round := range 10 {
		const senders = 8
		tokens := make([]string, senders)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range senders {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				sent, ok, err := svc.IssueGuestLink(ctx, ev)
				if err != nil || !ok {
					t.Errorf("send %d: ok=%v err=%v", i, ok, err)
					return
				}
				tokens[i] = sent.GuestToken
			}()
		}
		close(start)
		wg.Wait()

		working := 0
		for _, tok := range tokens {
			if _, err := svc.TicketForGuestToken(ctx, tok); err == nil {
				working++
			}
		}
		require.Equal(t, 1, working, "round %d: %d of %d concurrently sent links work", round, working, senders)
	}
}
