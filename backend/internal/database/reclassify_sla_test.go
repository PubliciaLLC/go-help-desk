package database_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Reclassifying a ticket against real rows (1.3.0 audit D3): the real
// MatchSLAPolicy picks the policy for the new category, the clock and the met
// targets are carried over, and the audit entry shares the change's
// transaction. The unit tests for the rules are in
// internal/domain/ticket/reclassify_sla_test.go; this is the SQL and the
// transaction.

// secondCategory is a category with its own medium policy, so a ticket moved
// into it re-matches to a policy it was not under.
func (f *changePriorityFx) secondCategory(t *testing.T) (category.Category, sla.Policy) {
	t.Helper()
	cat := category.Category{ID: uuid.New(), Name: "Reclassify " + uuid.NewString()[:8], SortOrder: 2, Active: true}
	require.NoError(t, categorystore.New(f.q).CreateCategory(f.ctx, cat))
	medium := ticket.PriorityMedium
	pol := sla.Policy{
		ID: uuid.New(), Name: "reclassify medium", Priority: &medium, CategoryID: &cat.ID,
		ResponseTargetMin: 15, ResolutionTargetMin: 60,
	}
	require.NoError(t, f.ss.CreatePolicy(f.ctx, pol))
	// Runs before the fixture's own cleanup (LIFO), which would otherwise still
	// find the ticket pointing at this category.
	t.Cleanup(func() {
		_, _ = f.db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, f.tk.ID)
		_, _ = f.db.SQL.Exec(`DELETE FROM sla_policies WHERE category_id = $1`, cat.ID)
		_, _ = f.db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})
	return cat, pol
}

func TestReclassify_RematchesAndAuditsAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	cat, pol := f.secondCategory(t)

	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, recordIDOf(t, f.ctx, f.ss, f.tk.ID), time.Now().UTC().Truncate(time.Millisecond), 600, 1800))
	oldRow := f.records(t)[0]

	got, err := svc.UpdateCTI(f.ctx, f.tk.ID, cat.ID, nil, nil, f.staff)
	require.NoError(t, err)
	require.Equal(t, cat.ID, got.CategoryID)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, cat.ID, stored.CategoryID)

	rows := f.records(t)
	require.Len(t, rows, 2)
	cur, old := rows[0], rows[1]
	require.Equal(t, oldRow.ID, old.ID)
	require.True(t, old.SupersededAt.Valid, "the old record is superseded, not edited")
	require.False(t, cur.SupersededAt.Valid)
	require.Equal(t, pol.ID, cur.PolicyID, "the policy for the new category")
	require.Equal(t, oldRow.StartedAt, cur.StartedAt, "the clock start is kept")
	require.Equal(t, oldRow.FirstResponseAt, cur.FirstResponseAt, "a target already met stays met")

	require.Equal(t, map[string]int{"classified": 1}, f.actions(t))
	entries, err := f.au.ListByEntity(f.ctx, "ticket", f.tk.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].ActorID)
	require.Equal(t, *f.staff.UserID, *entries[0].ActorID)
	require.Equal(t, string(ticket.SLARematched), entries[0].After["sla_outcome"])
	require.Equal(t, cat.ID.String(), entries[0].After["category_id"])
	require.Equal(t, f.tk.CategoryID.String(), entries[0].Before["category_id"])
}

// The audit write is part of the change: failing it after the category, the
// supersede and the new record have been written leaves the database as it was.
func TestReclassify_AuditFailureRollsBackAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, failingAuditAtomic{db: f.db.SQL, failAction: "classified"})
	cat, _ := f.secondCategory(t)

	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	rowsBefore := f.records(t)

	_, err := svc.UpdateCTI(f.ctx, f.tk.ID, cat.ID, nil, nil, f.staff)
	require.ErrorIs(t, err, errAuditDown)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, f.tk.CategoryID, stored.CategoryID, "the category is rolled back")
	require.Equal(t, rowsBefore, f.records(t), "no supersede, no new record")
	require.Empty(t, f.actions(t))
}
