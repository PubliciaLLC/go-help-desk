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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #445: the per-ticket cap on guest files is exact. With one slot left, several
// uploads racing for it leave exactly one file there: the count is taken in the
// same transaction that holds the ticket row, not before it.
//
// Real Postgres on separate connections, repeated, because the defect is a
// check-then-insert gap and a single connection would serialise it away.
func TestCreateGuestAttachment_ParallelUploadsCannotOvershootTheCap(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	q := db.Queries
	cs, ts := categorystore.New(q), ticketstore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "Cap race " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	svc := ticket.NewService(ts, ts, notification.Noop{}, auditstore.New(q), txrunner.New(db.SQL, nil, nil), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))
	t.Cleanup(func() { _, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID) })

	guest := ticket.Actor{Role: user.RoleUser}
	const capN, racers = 3, 6
	for round := range 15 {
		email := "cap-" + uuid.NewString()[:8] + "@test.local"
		tk := ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain,
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("CAP-" + uuid.NewString()[:8]),
			Subject: "Racing uploads", Description: "x", CategoryID: cat.ID,
			Priority: ticket.PriorityLow, StatusID: newSt.ID, GuestEmail: &email, GuestName: "Ada",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, ts.Create(ctx, tk))
		t.Cleanup(func() { _, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, tk.ID) })

		att := func() ticket.Attachment {
			return ticket.Attachment{
				ID: uuid.New(), TicketID: tk.ID, Filename: "f.txt", MimeType: "text/plain",
				SizeBytes: 1, StoragePath: "x", CreatedAt: time.Now().UTC(), UploadedByGuest: true,
			}
		}
		for range capN - 1 {
			require.NoError(t, svc.CreateGuestAttachment(ctx, att(), guest, capN))
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, racers)
		for range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- svc.CreateGuestAttachment(ctx, att(), guest, capN)
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		won := 0
		for err := range results {
			if err == nil {
				won++
			} else {
				require.ErrorIs(t, err, ticket.ErrGuestAttachmentLimit, "round %d", round)
			}
		}
		n, err := svc.CountGuestAttachments(ctx, tk.ID)
		require.NoError(t, err)
		require.Equal(t, capN, n, "round %d: exactly the cap, never more", round)
		require.Equal(t, 1, won, "round %d: one winner", round)
	}
}
