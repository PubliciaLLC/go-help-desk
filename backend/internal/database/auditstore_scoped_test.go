package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Filter.ScopedTo puts the staff visibility rule inside the audit query, so the
// page, the offset and the total are one sequence. server's parity test holds
// the rule equal to ticket.CanView across a matrix; this pins what only a
// store can: what the query itself does with entries that are not on a visible
// ticket, and that the page and the count agree.
func TestAuditStore_Search_ScopedTo(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q) // staff: assigned one ticket, their group scopes catB
	aus := auditstore.New(q)

	const action = "scoped_fixture"
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	plant := func(entityType string, entityID uuid.UUID, offset time.Duration) uuid.UUID {
		id := uuid.New()
		require.NoError(t, q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
			ID: id, EntityType: entityType, EntityID: entityID, Action: action, CreatedAt: at.Add(offset),
		}))
		return id
	}
	assigned := plant("ticket", f.assignedTicket.ID, 1*time.Second)
	scoped := plant("ticket", f.scopedTicket.ID, 2*time.Second)
	plant("ticket", f.strangerTicket.ID, 3*time.Second)
	plant("ticket", f.reporterTicket.ID, 4*time.Second)
	// A ticket that does not exist, and a non-ticket entry whose id IS a ticket
	// the staff member can see: neither is a ticket entry on a visible ticket.
	plant("ticket", uuid.New(), 5*time.Second)
	plant("user", f.assignedTicket.ID, 6*time.Second)

	byAction := audit.Filter{Action: action}

	t.Run("unscoped sees every entry, as before", func(t *testing.T) {
		got, total, err := aus.Search(ctx, byAction, 50, 0)
		require.NoError(t, err)
		require.Equal(t, 6, total)
		require.Len(t, got, 6)
	})

	t.Run("scoped returns only entries on visible tickets, newest first", func(t *testing.T) {
		fl := byAction
		fl.ScopedTo = &f.staff.ID
		got, total, err := aus.Search(ctx, fl, 50, 0)
		require.NoError(t, err)
		require.Equal(t, 2, total, "the count must be of what the page pages")
		require.Len(t, got, 2)
		require.Equal(t, []uuid.UUID{scoped, assigned}, []uuid.UUID{got[0].ID, got[1].ID})
	})

	t.Run("the offset indexes the visible sequence", func(t *testing.T) {
		fl := byAction
		fl.ScopedTo = &f.staff.ID
		got, total, err := aus.Search(ctx, fl, 1, 1)
		require.NoError(t, err)
		require.Equal(t, 2, total)
		require.Len(t, got, 1)
		require.Equal(t, assigned, got[0].ID)
	})

	t.Run("someone with no ticket and no group sees nothing", func(t *testing.T) {
		nobody := uuid.New()
		fl := byAction
		fl.ScopedTo = &nobody
		got, total, err := aus.Search(ctx, fl, 50, 0)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, got)
	})

	t.Run("filters still apply alongside scope", func(t *testing.T) {
		fl := audit.Filter{Action: "no_such_action", ScopedTo: &f.staff.ID}
		got, total, err := aus.Search(ctx, fl, 50, 0)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, got)

		from := at.Add(2500 * time.Millisecond) // after both visible entries
		fl = audit.Filter{Action: action, ScopedTo: &f.staff.ID, From: &from}
		got, total, err = aus.Search(ctx, fl, 50, 0)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, got)
	})
}
