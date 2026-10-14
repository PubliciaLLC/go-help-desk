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

// A restart followed by a trip through a priority with no policy (Erik,
// 2026-10-14) resumes the clock from the restart, on real rows, and the "SLA
// restarted" badge still points at the pre-restart record.
func TestReopen_TheDanceResumesTheRestartAndTheBadgeStillPointsBeforeIt(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	f.resolvedWithMetSLA(t, svc)
	preRestart := f.records(t)[0]

	_, err := svc.UpdateStatus(f.ctx, f.tk.ID, f.newStatusID(t), f.staff) // reopen: high, restart
	require.NoError(t, err)
	restart := f.records(t)[0]
	require.True(t, restart.StartedAt.Valid)

	// low has no policy in the fixture.
	_, err = svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityLow, f.staff)
	require.NoError(t, err)
	_, err = svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)

	cur := f.records(t)[0]
	require.False(t, cur.SupersededAt.Valid)
	require.True(t, cur.StartedAt.Valid, "NULL would put the clock back at the ticket's creation")
	require.True(t, restart.StartedAt.Time.Equal(cur.StartedAt.Time), "resumes from the restart")
	require.Equal(t, restart.PausedSecondsAtStart, cur.PausedSecondsAtStart)
	require.False(t, cur.FirstResponseAt.Valid, "no facts copied from the superseded record")

	history, err := f.ss.ListRecordsForTicket(f.ctx, f.tk.ID)
	require.NoError(t, err)
	tk, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	earlier := sla.EarlierRecord(history, tk.CreatedAt)
	require.NotNil(t, earlier, "the badge is still there")
	require.Equal(t, preRestart.PolicyID, earlier.PolicyID, "and points at the pre-restart record")
	require.Equal(t, sla.EarlierMet, earlier.Outcome)
}

// The dance on a ticket that was never reopened produces no badge, and its
// clock stays the original one, from creation.
func TestReopen_TheDanceAloneNeverProducesABadge(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 600, 1800))

	for _, p := range []ticket.Priority{ticket.PriorityLow, ticket.PriorityHigh, ticket.PriorityLow, ticket.PriorityMedium} {
		_, err := svc.ChangePriority(f.ctx, f.tk.ID, p, f.staff)
		require.NoError(t, err)
	}

	cur := f.records(t)[0]
	require.False(t, cur.SupersededAt.Valid)
	require.False(t, cur.StartedAt.Valid, "still the ticket's creation")
	history, err := f.ss.ListRecordsForTicket(f.ctx, f.tk.ID)
	require.NoError(t, err)
	tk, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Nil(t, sla.EarlierRecord(history, tk.CreatedAt))
}

// A ticket that never had a record still starts from its creation when a
// priority change gives it one.
func TestReopen_ATicketThatNeverHadARecordStillStartsFromCreation(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)

	_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)

	rows := f.records(t)
	require.Len(t, rows, 1)
	require.False(t, rows[0].StartedAt.Valid)
}
