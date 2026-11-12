package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// One sweep batch deletes at most batch_size tokens, whatever plan Postgres
// picks. The statement used to be `id IN (SELECT ... LIMIT n FOR UPDATE SKIP
// LOCKED)`, and under a nested-loop semi join the subquery was re-run per row of
// the table, so a batch of 3 could delete every expired token. The transaction
// and the forced plan keep this deterministic; see
// testutil.ForceRescannedBatchPlan.
func TestDeleteExpiredGuestAccessTokens_NeverDeletesMoreThanItsBatch(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	testutil.ForceRescannedBatchPlan(t, tx)
	ctx := context.Background()
	ts := ticketstore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "Token batch " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	email := "token-batch-" + uuid.NewString()[:8] + "@test.local"
	tk := ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("TKB-" + uuid.NewString()[:8]),
		Subject: "t", Description: "t", CategoryID: cat.ID, Priority: ticket.PriorityLow,
		StatusID: newSt.ID, GuestEmail: &email, GuestName: "Ada",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))

	for i := range 7 {
		require.NoError(t, ts.CreateGuestToken(ctx, uuid.New(), tk.ID, "token-batch-"+string(rune('a'+i)),
			time.Date(1970, 1, 1, 0, 0, i, 0, time.UTC)))
	}
	n, err := q.DeleteExpiredGuestAccessTokens(ctx, 3)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)

	var left int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM guest_access_tokens WHERE token_hash LIKE 'token-batch-%'`).Scan(&left))
	require.Equal(t, 4, left)
}
