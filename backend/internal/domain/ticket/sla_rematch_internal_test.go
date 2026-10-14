package ticket

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// pausedSecondsAt is the pause total a restarted SLA record carries as its base
// (#443b): the ticket's closed Pending intervals, plus the part of an open one
// already elapsed. It must agree with sla.Elapsed's clipping, so an interval
// that opens after `at` contributes nothing.
func TestPausedSecondsAt(t *testing.T) {
	base := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	since := func(d time.Duration) *time.Time { v := base.Add(d); return &v }

	cases := []struct {
		name string
		t    Ticket
		at   time.Time
		want int64
	}{
		{name: "not pending, nothing paused", t: Ticket{}, at: base, want: 0},
		{name: "not pending, closed intervals only", t: Ticket{SLAPausedSeconds: 300}, at: base, want: 300},
		{
			name: "pending since T, at T+90s",
			t:    Ticket{SLAPausedSeconds: 300, PendingSince: since(0)},
			at:   base.Add(90 * time.Second),
			want: 390,
		},
		{
			name: "pending with nothing closed before",
			t:    Ticket{PendingSince: since(0)},
			at:   base.Add(time.Hour),
			want: 3600,
		},
		{
			name: "pending that opens after at is clipped away",
			t:    Ticket{SLAPausedSeconds: 300, PendingSince: since(time.Minute)},
			at:   base,
			want: 300,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pausedSecondsAt(tc.t, tc.at))
		})
	}
}

// planSLARematch is DESIGN.md → SLA Tracking → "Re-matching when priority
// changes" as a table. Every row of it is a case here.
func TestPlanSLARematch(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3 * time.Hour)
	pending := now.Add(-10 * time.Minute)

	policyA, policyB := uuid.New(), uuid.New()
	tk := Ticket{ID: uuid.New(), SLAPausedSeconds: 600}
	pendingTk := Ticket{ID: tk.ID, SLAPausedSeconds: 600, PendingSince: &pending}

	running := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started, PausedSecondsAtStart: 120}
	runningFromCreation := &SLARecordState{ID: uuid.New(), PolicyID: policyA}
	settled := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started, PausedSecondsAtStart: 120, Settled: true}

	cases := []struct {
		name    string
		enabled bool
		open    bool
		cur     *SLARecordState
		matched *uuid.UUID
		tk      Ticket
		want    SLAOutcome
		// wantStart describes the new record; nil means none is started.
		wantStart *SLARecordStart
	}{
		{name: "switched off", enabled: false, open: true, cur: running, matched: &policyB, tk: tk, want: SLAOff},
		{name: "switched off beats not open", enabled: false, open: false, cur: running, matched: &policyB, tk: tk, want: SLAOff},
		{name: "resolved or closed", enabled: true, open: false, cur: running, matched: &policyB, tk: tk, want: SLANotOpen},
		{name: "no record, no policy", enabled: true, open: true, tk: tk, want: SLANone},
		{
			name: "no record, policy matches: clock starts at the change", enabled: true, open: true,
			matched: &policyB, tk: tk, want: SLAAttached,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &now, PausedSecondsAtStart: 600},
		},
		{
			name: "no record, policy matches, ticket pending: base includes the open interval", enabled: true, open: true,
			matched: &policyB, tk: pendingTk, want: SLAAttached,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &now, PausedSecondsAtStart: 1200},
		},
		{name: "same policy, running", enabled: true, open: true, cur: running, matched: &policyA, tk: tk, want: SLAUnchanged},
		{name: "same policy, settled", enabled: true, open: true, cur: settled, matched: &policyA, tk: tk, want: SLAUnchanged},
		{
			name: "running record, other policy: the clock is inherited", enabled: true, open: true,
			cur: running, matched: &policyB, tk: tk, want: SLARematched,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &started, PausedSecondsAtStart: 120},
		},
		{
			name: "running record from creation, other policy: still from creation", enabled: true, open: true,
			cur: runningFromCreation, matched: &policyB, tk: tk, want: SLARematched,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: nil, PausedSecondsAtStart: 0},
		},
		{
			name: "settled record, other policy: a new clock from the change", enabled: true, open: true,
			cur: settled, matched: &policyB, tk: tk, want: SLARestarted,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &now, PausedSecondsAtStart: 600},
		},
		{
			name: "settled record, other policy, ticket pending", enabled: true, open: true,
			cur: settled, matched: &policyB, tk: pendingTk, want: SLARestarted,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &now, PausedSecondsAtStart: 1200},
		},
		{name: "running record, no policy matches", enabled: true, open: true, cur: running, tk: tk, want: SLARemoved},
		{name: "settled record, no policy matches", enabled: true, open: true, cur: settled, tk: tk, want: SLARemoved},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, start := planSLARematch(tc.enabled, tc.open, tc.cur, tc.matched, tc.tk, now)
			require.Equal(t, tc.want, got)
			if tc.wantStart == nil {
				require.Nil(t, start)
				return
			}
			require.NotNil(t, start)
			require.NotEqual(t, uuid.Nil, start.ID, "the new record gets its own id")
			want := *tc.wantStart
			want.ID = start.ID
			require.Equal(t, want, *start)
		})
	}
}
