package database_test

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A staff re-match copies the at-risk stamps (#670) onto the new record exactly
// as it copies the breach stamps, so a crossing already announced is not
// announced again; a reopen starts clean; `attached` resumes the clock with no
// facts. These run the whole ChangePriority / reopen path against real rows.

// stampedRecord creates the fixture ticket's record under the medium policy and
// stamps it with req, returning the row as stored.
func (f *changePriorityFx) stampedRecord(t *testing.T, reqs ...ticket.SLAStampRequest) dbgen.SlaRecord {
	t.Helper()
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	id := recordIDOf(t, f.ctx, f.ss, f.tk.ID)
	for i, req := range reqs {
		req.At = time.Now().UTC().Truncate(time.Millisecond).Add(time.Duration(i) * time.Second)
		_, err := f.ts.StampSLACrossings(f.ctx, id, req)
		require.NoError(t, err)
	}
	return f.records(t)[0]
}

// current is the one record that is not superseded.
func current(t *testing.T, rows []dbgen.SlaRecord) dbgen.SlaRecord {
	t.Helper()
	var out []dbgen.SlaRecord
	for _, r := range rows {
		if !r.SupersededAt.Valid {
			out = append(out, r)
		}
	}
	require.Len(t, out, 1, "exactly one record is current")
	return out[0]
}

func requireNoStamps(t *testing.T, r dbgen.SlaRecord) {
	t.Helper()
	require.False(t, r.ResponseAtRiskAt.Valid)
	require.False(t, r.ResolutionAtRiskAt.Valid)
	require.False(t, r.ResponseBreachedAt.Valid)
	require.False(t, r.ResolutionBreachedAt.Valid)
}

func TestRematch_CarriesTheAtRiskStampsAgainstRealRows(t *testing.T) {
	cases := []struct {
		name string
		reqs []ticket.SLAStampRequest
	}{
		{"response at risk", []ticket.SLAStampRequest{{ResponseAtRisk: true}}},
		{"resolution at risk", []ticket.SLAStampRequest{{ResolutionAtRisk: true}}},
		{"both at risk", []ticket.SLAStampRequest{{ResponseAtRisk: true, ResolutionAtRisk: true}}},
		{
			// A target that went at risk and later breached keeps both stamps.
			name: "at risk, then breached",
			reqs: []ticket.SLAStampRequest{{ResponseAtRisk: true}, {ResponseBreach: true, ResolutionAtRisk: true}},
		},
		{"breached only", []ticket.SLAStampRequest{{ResponseBreach: true, ResolutionBreach: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newChangePriorityFx(t)
			svc := f.service(t, nil)
			old := f.stampedRecord(t, tc.reqs...)

			_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
			require.NoError(t, err)

			rows := f.records(t)
			require.Len(t, rows, 2)
			cur := current(t, rows)
			require.NotEqual(t, old.ID, cur.ID)
			require.Equal(t, f.pol[ticket.PriorityHigh].ID, cur.PolicyID)
			require.Equal(t, old.ResponseAtRiskAt, cur.ResponseAtRiskAt)
			require.Equal(t, old.ResolutionAtRiskAt, cur.ResolutionAtRiskAt)
			require.Equal(t, old.ResponseBreachedAt, cur.ResponseBreachedAt)
			require.Equal(t, old.ResolutionBreachedAt, cur.ResolutionBreachedAt)

			// And the old record keeps its own, untouched.
			var was dbgen.SlaRecord
			for _, r := range rows {
				if r.ID == old.ID {
					was = r
				}
			}
			was.SupersededAt = sql.NullTime{}
			require.Equal(t, old, was)
		})
	}
}

// A crossing the re-match carried is not set again: a second stamp on the new
// record reports nothing newly set, so nothing is announced twice.
func TestRematch_ACarriedAtRiskStampIsNotStampedAgain(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	old := f.stampedRecord(t, ticket.SLAStampRequest{ResponseAtRisk: true})

	_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)
	cur := current(t, f.records(t))

	got, err := f.ts.StampSLACrossings(f.ctx, cur.ID, ticket.SLAStampRequest{
		At: time.Now().UTC(), ResponseAtRisk: true, ResolutionAtRisk: true,
	})
	require.NoError(t, err)
	require.False(t, got.ResponseAtRisk, "already announced before the re-match")
	require.True(t, got.ResolutionAtRisk, "a crossing that had not happened yet still can")
	after := current(t, f.records(t))
	require.Equal(t, old.ResponseAtRiskAt, after.ResponseAtRiskAt, "the first stamp's time is kept")
}

// A stamp racing a re-match lands before it (and is copied) or finds the record
// superseded and writes nothing: never lost, never duplicated.
func TestRematch_RacingStampSLACrossingsNeverLosesOrDuplicatesAStamp(t *testing.T) {
	f := newChangePriorityFx(t) // committed rows, a database of its own
	svc := f.service(t, nil)
	prios := []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical}
	var before, after int

	for round := 0; round < 20; round++ {
		tk := f.tk
		tk.ID = uuid.New()
		tk.TrackingNumber = ticket.TrackingNumber("AR-" + uuid.NewString()[:8])
		require.NoError(t, f.ts.Create(f.ctx, tk))
		require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
		oldID := recordIDOf(t, f.ctx, f.ss, tk.ID)

		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			stamped ticket.SLAStamped
			stErr   error
			rmErr   error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			// Offset the stamp across the re-match's window, so both orders occur.
			time.Sleep(time.Duration(round%10) * 400 * time.Microsecond)
			stamped, stErr = f.ts.StampSLACrossings(f.ctx, oldID, ticket.SLAStampRequest{
				At: time.Now().UTC(), ResponseAtRisk: true, ResolutionBreach: true,
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, rmErr = svc.ChangePriority(f.ctx, tk.ID, prios[round%len(prios)], f.staff)
		}()
		close(start)
		wg.Wait()
		require.NoError(t, rmErr)

		rows, err := f.q.ListSLARecordsForTicket(f.ctx, tk.ID)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		cur := current(t, rows)
		var old dbgen.SlaRecord
		for _, r := range rows {
			if r.ID == oldID {
				old = r
			}
		}
		require.True(t, old.SupersededAt.Valid)

		if stErr == nil {
			// The stamp landed first: announced once, on the old record, and
			// copied to the new one, which therefore announces nothing again.
			before++
			require.True(t, stamped.ResponseAtRisk, "round %d", round)
			require.True(t, stamped.ResolutionBreach, "round %d", round)
			require.True(t, old.ResponseAtRiskAt.Valid && old.ResolutionBreachedAt.Valid, "round %d", round)
			require.Equal(t, old.ResponseAtRiskAt, cur.ResponseAtRiskAt, "round %d: lost by the re-match", round)
			require.Equal(t, old.ResolutionBreachedAt, cur.ResolutionBreachedAt, "round %d: lost by the re-match", round)
			again, err := f.ts.StampSLACrossings(f.ctx, cur.ID, ticket.SLAStampRequest{
				At: time.Now().UTC(), ResponseAtRisk: true, ResolutionBreach: true,
			})
			require.NoError(t, err)
			require.False(t, again.ResponseAtRisk, "round %d: announced twice", round)
			require.False(t, again.ResolutionBreach, "round %d: announced twice", round)
			continue
		}
		// The re-match won: the stamp matched zero rows and nothing was written
		// to either record. The next pass evaluates the new one and announces.
		after++
		require.True(t, errors.Is(stErr, ticket.ErrSLARecordSuperseded), "round %d: %v", round, stErr)
		requireNoStamps(t, old)
		requireNoStamps(t, cur)
	}
	t.Logf("stamp first in %d rounds, re-match first in %d", before, after)
}

// The re-match first, made certain: it holds the record's lock while the stamp
// is issued, so the stamp waits, then finds the record superseded and writes
// nothing to either record.
func TestRematch_AStampWaitingOnTheLockFindsTheRecordSuperseded(t *testing.T) {
	f := newChangePriorityFx(t)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	oldID := recordIDOf(t, f.ctx, f.ss, f.tk.ID)

	tx, err := f.db.SQL.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	rm := ticketstore.New(dbgen.New(tx))
	cur, err := rm.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, oldID, cur.ID)

	done := make(chan error, 1)
	go func() {
		_, err := f.ts.StampSLACrossings(f.ctx, oldID, ticket.SLAStampRequest{
			At: time.Now().UTC(), ResponseAtRisk: true, ResolutionBreach: true,
		})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the stamp did not wait for the re-match's lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, rm.SupersedeSLARecord(f.ctx, oldID, time.Now().UTC()))
	require.NoError(t, rm.StartSLARecord(f.ctx, ticket.SLARecordStart{
		ID: uuid.New(), TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityHigh].ID, Facts: cur.Facts,
	}))
	require.NoError(t, tx.Commit())

	require.ErrorIs(t, <-done, ticket.ErrSLARecordSuperseded)
	for _, r := range f.records(t) {
		requireNoStamps(t, r)
	}
}

// A reopen restarts clean: the new record has no stamps, so it can be at risk
// and breach, and be announced, again. The old record keeps its stamps.
func TestReopen_StartsCleanWithNoAtRiskStamps(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	old := f.stampedRecord(t, ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionAtRisk: true},
		ticket.SLAStampRequest{ResolutionBreach: true})
	_, err := svc.Resolve(f.ctx, f.tk.ID, "done", f.staff)
	require.NoError(t, err)

	_, err = svc.UpdateStatus(f.ctx, f.tk.ID, f.newStatusID(t), f.staff)
	require.NoError(t, err)

	rows := f.records(t)
	require.Len(t, rows, 2)
	cur := current(t, rows)
	require.NotEqual(t, old.ID, cur.ID)
	require.True(t, cur.StartedAt.Valid, "a restart")
	requireNoStamps(t, cur)

	for _, r := range rows {
		if r.ID == old.ID {
			require.True(t, r.ResponseAtRiskAt.Valid && r.ResolutionAtRiskAt.Valid && r.ResolutionBreachedAt.Valid,
				"history keeps its stamps")
		}
	}

	// The new record can cross, and be announced, again.
	got, err := f.ts.StampSLACrossings(f.ctx, cur.ID, ticket.SLAStampRequest{
		At: time.Now().UTC(), ResponseAtRisk: true, ResolutionAtRisk: true,
	})
	require.NoError(t, err)
	require.True(t, got.ResponseAtRisk)
	require.True(t, got.ResolutionAtRisk)
}

// `attached` resumes the old clock and none of its facts, at-risk stamps
// included: its crossings are announced if fresh and stamped silently if not.
func TestRematch_AttachedResumesTheClockWithNoStamps(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	old := f.stampedRecord(t, ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionBreach: true})

	// low has no policy in the fixture: the record is removed, then attached.
	_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityLow, f.staff)
	require.NoError(t, err)
	_, err = svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)

	rows := f.records(t)
	require.Len(t, rows, 2)
	cur := current(t, rows)
	require.NotEqual(t, old.ID, cur.ID)
	require.Equal(t, old.StartedAt, cur.StartedAt, "the old clock resumes")
	requireNoStamps(t, cur)
}
