package database_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A reopen against real Postgres (#464): the flag, the priority bump, the SLA
// restart and every audit entry are one transaction, and the count survives a
// round trip through tickets.reopen_count.

// resolvedWithMetSLA puts the fixture's ticket in Resolved with a medium-policy
// record that has met its response target.
func (f *changePriorityFx) resolvedWithMetSLA(t *testing.T, svc *ticket.Service) {
	t.Helper()
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 600, 1800))
	_, err := svc.Resolve(f.ctx, f.tk.ID, "done", f.staff)
	require.NoError(t, err)
}

func (f *changePriorityFx) newStatusID(t *testing.T) uuid.UUID {
	t.Helper()
	st, err := f.ts.GetStatusByName(f.ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	return st.ID
}

func TestReopen_PersistsTheFlagTheBumpAndTheRestartAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	f.resolvedWithMetSLA(t, svc)
	oldRow := f.records(t)[0]

	got, err := svc.UpdateStatus(f.ctx, f.tk.ID, f.newStatusID(t), f.staff)
	require.NoError(t, err)
	require.Equal(t, 1, got.ReopenCount)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.ReopenCount, "round-trips through tickets.reopen_count")
	require.Equal(t, ticket.PriorityHigh, stored.Priority)

	rows := f.records(t)
	require.Len(t, rows, 2)
	cur, old := rows[0], rows[1]
	require.Equal(t, oldRow.ID, old.ID)
	require.True(t, old.SupersededAt.Valid, "the old record is kept, superseded")
	require.False(t, old.FirstResponseAt.Time.IsZero(), "with its facts")
	require.False(t, cur.SupersededAt.Valid)
	require.Equal(t, f.pol[ticket.PriorityHigh].ID, cur.PolicyID)
	require.False(t, cur.FirstResponseAt.Valid, "both targets timed anew")
	require.True(t, cur.StartedAt.Valid, "a restart has a start; NULL would mean ticket creation")

	a := f.actions(t)
	require.Equal(t, 1, a["reopened"])
	require.Equal(t, 1, a["priority_changed"])
	require.Equal(t, 1, a["sla_restarted"])

	// Resolve and reopen again: flagged again, restarted again, not bumped again.
	_, err = svc.Resolve(f.ctx, f.tk.ID, "done again", f.staff)
	require.NoError(t, err)
	got, err = svc.UpdateStatus(f.ctx, f.tk.ID, f.newStatusID(t), f.staff)
	require.NoError(t, err)
	require.Equal(t, 2, got.ReopenCount)
	require.Equal(t, ticket.PriorityHigh, got.Priority)
	a = f.actions(t)
	require.Equal(t, 2, a["reopened"])
	require.Equal(t, 1, a["priority_changed"])
	require.Equal(t, 2, a["sla_restarted"])
	require.Len(t, f.records(t), 3)
}

// Each store call a reopen makes is failed in turn after its real write: the
// status, the flag, the priority, the SLA rows and the audit entries must all be
// as they were.
func TestReopen_EachFailingStepRollsEverythingBack(t *testing.T) {
	errStep := errors.New("injected failure")
	for _, step := range []string{"Update", "CurrentSLARecordForUpdate", "MatchSLAPolicy", "SupersedeSLARecord", "StartSLARecord", "audit"} {
		t.Run(step, func(t *testing.T) {
			f := newChangePriorityFx(t)
			f.resolvedWithMetSLA(t, f.service(t, nil))
			rowsBefore := f.records(t)
			ticketBefore, err := f.ts.GetByID(f.ctx, f.tk.ID)
			require.NoError(t, err)
			actionsBefore := f.actions(t)

			svc := f.service(t, failingStepAtomic{db: f.db.SQL, step: step, err: errStep})
			_, err = svc.UpdateStatus(f.ctx, f.tk.ID, f.newStatusID(t), f.staff)
			require.ErrorIs(t, err, errStep)

			stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
			require.NoError(t, err)
			require.Zero(t, stored.ReopenCount)
			require.Equal(t, ticketBefore.Priority, stored.Priority)
			require.Equal(t, ticketBefore.StatusID, stored.StatusID)
			require.True(t, ticketBefore.UpdatedAt.Equal(stored.UpdatedAt))
			require.Equal(t, rowsBefore, f.records(t), "no supersede, no new record")
			require.Equal(t, actionsBefore, f.actions(t), "no audit entry survives")
		})
	}
}

// The column refuses a negative count; the default is 0 for a ticket created
// before the migration or since.
func TestReopen_CountDefaultsToZeroAndRefusesNegative(t *testing.T) {
	f := newChangePriorityFx(t)
	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Zero(t, stored.ReopenCount)

	_, err = f.db.SQL.Exec(`UPDATE tickets SET reopen_count = -1 WHERE id = $1`, f.tk.ID)
	require.Error(t, err)
}
