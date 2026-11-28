package database_test

import (
	"context"
	"strconv"
	"strings"
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

const (
	migration064Up   = "migrations/000064_ticket_links_follow_up.up.sql"
	migration064Down = "migrations/000064_ticket_links_follow_up.down.sql"
)

// Migration 000064 adds ticket_links.follow_up (#625). It runs here against
// TEMP stand-ins for tickets and ticket_links, verbatim:
//
//   - a parent_child row whose child has the same reporting account, or the same
//     guest address (any case), as its parent is backfilled TRUE, so every real
//     follow-up keeps counting as a requester's one;
//   - a parent_child row between two different people's tickets reads FALSE: it
//     is not a follow-up, and flagged it would name someone else's ticket on the
//     closed ticket's page;
//   - every other link type reads FALSE;
//   - a row inserted afterwards without the column is FALSE (a link made by hand);
//   - the down migration removes the column again.
func TestMigration064_BackfillsOnlyLinksBetweenOneRequestersTickets(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`CREATE TEMP TABLE tickets (id int, reporter_user_id int, guest_email text) ON COMMIT DROP`,
		`INSERT INTO tickets VALUES
			(1, 7, NULL), (2, 7, NULL), (3, 8, NULL),
			(4, NULL, 'Ada@Example.test'), (5, NULL, 'ada@example.test'), (6, NULL, 'bob@example.test'),
			(7, NULL, NULL), (8, NULL, NULL)`,
		`CREATE TEMP TABLE ticket_links (id int, source_ticket_id int, target_ticket_id int, link_type text) ON COMMIT DROP`,
		`INSERT INTO ticket_links VALUES
			(1, 1, 2, 'parent_child'),
			(2, 1, 3, 'parent_child'),
			(3, 4, 5, 'parent_child'),
			(4, 4, 6, 'parent_child'),
			(5, 1, 4, 'parent_child'),
			(6, 7, 8, 'parent_child'),
			(7, 1, 2, 'related_to'),
			(8, 1, 3, 'duplicate_of')`,
	} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}

	execMigrationFile(t, ctx, tx, migration064Up, nil)

	flagged := map[int]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT id, follow_up FROM ticket_links`)
	require.NoError(t, err)
	for rows.Next() {
		var id int
		var f bool
		require.NoError(t, rows.Scan(&id, &f))
		flagged[id] = f
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[int]bool{
		1: true,  // same reporting account
		2: false, // two accounts
		3: true,  // the same guest address, in another case
		4: false, // two guests
		5: false, // an account and a guest
		6: false, // no requester on either: nothing says they are one person's
		7: false, 8: false,
	}, flagged)

	_, err = tx.ExecContext(ctx, `INSERT INTO ticket_links (id, source_ticket_id, target_ticket_id, link_type) VALUES (9, 1, 2, 'parent_child')`)
	require.NoError(t, err)
	var f bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT follow_up FROM ticket_links WHERE id = 9`).Scan(&f))
	require.False(t, f, "a link made after the migration, by hand, is not a follow-up")

	execMigrationFile(t, ctx, tx, migration064Down, nil)
	var n int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
		AND table_name = 'ticket_links' AND column_name = 'follow_up'`).Scan(&n))
	require.Zero(t, n, "the down migration removes the column")
}

// The store carries the flag both ways, and the expired-token sweep deletes
// exactly the rows past their expiry.
func TestTicketStore_FollowUpLinkFlagAndTheTokenSweep(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	q := db.Queries
	ts := ticketstore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "Follow-up limits " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	mk := func(n string) ticket.Ticket {
		email := n + "-" + uuid.NewString()[:8] + "@test.local"
		tk := ticket.Ticket{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("FUL-" + uuid.NewString()[:8]),
			Subject: n, Description: n, CategoryID: cat.ID, Priority: ticket.PriorityLow,
			StatusID: newSt.ID, GuestEmail: &email, GuestName: "Ada",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}
	a, b, c := mk("a"), mk("b"), mk("c")

	t.Run("the flag survives a round trip", func(t *testing.T) {
		require.NoError(t, ts.CreateLink(ctx, ticket.TicketLink{
			SourceTicketID: a.ID, TargetTicketID: b.ID, LinkType: ticket.LinkParentChild, FollowUp: true}))
		require.NoError(t, ts.CreateLink(ctx, ticket.TicketLink{
			SourceTicketID: a.ID, TargetTicketID: c.ID, LinkType: ticket.LinkParentChild}))
		links, err := ts.ListLinks(ctx, a.ID)
		require.NoError(t, err)
		got := map[uuid.UUID]bool{}
		for _, l := range links {
			got[l.TargetTicketID] = l.FollowUp
		}
		require.Equal(t, map[uuid.UUID]bool{b.ID: true, c.ID: false}, got)
	})

	t.Run("the sweep deletes expired tokens and keeps live ones", func(t *testing.T) {
		now := time.Now()
		require.NoError(t, ts.CreateGuestToken(ctx, uuid.New(), a.ID, "expired-1", now.Add(-time.Minute)))
		require.NoError(t, ts.CreateGuestToken(ctx, uuid.New(), a.ID, "expired-2", now.Add(-24*time.Hour)))
		require.NoError(t, ts.CreateGuestToken(ctx, uuid.New(), a.ID, "live", now.Add(time.Hour)))

		n, err := ts.DeleteExpiredGuestTokens(ctx)

		require.NoError(t, err)
		require.EqualValues(t, 2, n)
		_, err = ts.TicketByGuestToken(ctx, "live")
		require.NoError(t, err)
		var left int
		require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM guest_access_tokens`).Scan(&left))
		require.Equal(t, 1, left)

		n, err = ts.DeleteExpiredGuestTokens(ctx)
		require.NoError(t, err)
		require.Zero(t, n, "nothing left to sweep")
	})

	// The first sweep after an upgrade meets every expired row at once. It goes
	// in batches (5,000, as the audit retention purge does), and a row somebody
	// else holds is left for the next sweep instead of waited on.
	t.Run("a backlog is deleted in batches, and a locked row is skipped not waited on", func(t *testing.T) {
		const backlog = 12001 // more than two batches, and not a multiple of one
		_, err := db.SQL.Exec(`INSERT INTO guest_access_tokens (id, ticket_id, token_hash, expires_at, created_at)
			SELECT gen_random_uuid(), $1, 'backlog-' || g, now() - interval '1 hour', now() - interval '31 days'
			FROM generate_series(1, $2) AS g`, a.ID, backlog)
		require.NoError(t, err)
		require.NoError(t, ts.CreateGuestToken(ctx, uuid.New(), a.ID, "live-2", time.Now().Add(time.Hour)))

		held, err := db.SQL.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = held.Rollback() }()
		var heldID uuid.UUID
		require.NoError(t, held.QueryRow(`SELECT id FROM guest_access_tokens WHERE token_hash = 'backlog-1' FOR UPDATE`).Scan(&heldID))

		// A deadline, so a sweep that waits on the held row fails here and does
		// not hang the suite.
		sweepCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		n, err := ts.DeleteExpiredGuestTokens(sweepCtx)

		require.NoError(t, err, "the sweep must not wait on a row another transaction holds")
		require.EqualValues(t, backlog-1, n)
		var left int
		require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM guest_access_tokens WHERE token_hash LIKE 'backlog-%'`).Scan(&left))
		require.Equal(t, 1, left, "only the held row is left")

		require.NoError(t, held.Commit())
		n, err = ts.DeleteExpiredGuestTokens(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "the next sweep takes it")
		_, err = ts.TicketByGuestToken(ctx, "live-2")
		require.NoError(t, err, "a live token is never swept")
	})
}

// #625 item 2, against real Postgres on separate connections: requesters who
// double-submit a follow-up. Exactly one wins, and the losers take no tracking
// number, so the tickets that exist are numbered without a gap. Before the
// number was drawn inside the transaction every loser had already taken one.
func TestRequesterFollowUp_ALostDoubleSubmitLeavesNoGapInTheNumbers(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()
	q := db.Queries
	ts := ticketstore.New(q)
	cat := category.Category{ID: uuid.New(), Name: "Double submit " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	svc := ticket.NewService(ts, ts, notification.Noop{}, auditstore.New(q), txrunner.New(db.SQL, nil, nil), nil)
	require.NoError(t, svc.LoadSystemStatuses(ctx))
	open := func(context.Context, uuid.UUID, *uuid.UUID) error { return nil }

	const rounds, clicks = 6, 4
	for round := range rounds {
		email := "double-" + uuid.NewString()[:8] + "@test.local"
		orig, err := svc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "Double submit", Description: "x", CategoryID: cat.ID, GuestEmail: &email, GuestName: "Ada", TrackingPrefix: "DBL"})
		require.NoError(t, err)
		require.NoError(t, svc.Close(ctx, orig.ID, ticket.Actor{Role: user.RoleAdmin}))

		errs := make([]error, clicks)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range clicks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = svc.CreateRequesterFollowUp(ctx, orig.ID, "DBL", ticket.ChannelGuest, ticket.Actor{Role: user.RoleUser}, open)
			}()
		}
		close(start)
		wg.Wait()

		won := 0
		for _, err := range errs {
			if err == nil {
				won++
				continue
			}
			require.ErrorIs(t, err, ticket.ErrFollowUpExists, "round %d", round)
		}
		require.Equal(t, 1, won, "round %d: exactly one follow-up", round)
	}

	rows, err := db.SQL.QueryContext(ctx, `SELECT tracking_number FROM tickets WHERE tracking_number LIKE 'DBL-%' ORDER BY tracking_number`)
	require.NoError(t, err)
	defer rows.Close()
	var seqs []int
	for rows.Next() {
		var tn string
		require.NoError(t, rows.Scan(&tn))
		n, err := strconv.Atoi(tn[strings.LastIndex(tn, "-")+1:])
		require.NoError(t, err)
		seqs = append(seqs, n)
	}
	require.NoError(t, rows.Err())
	require.Len(t, seqs, 2*rounds, "each round made the original and one follow-up")
	for i := 1; i < len(seqs); i++ {
		require.Equal(t, seqs[i-1]+1, seqs[i], "a lost double-submit burned a tracking number: %v", seqs)
	}
}
