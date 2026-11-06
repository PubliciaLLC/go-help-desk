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

// planSLARematch is a staff priority change's effect on the SLA (DESIGN.md → SLA
// Tracking → "Re-matching when priority changes") as a table. A staff change
// NEVER restarts a clock: the new record keeps the old one's start, pause base
// and facts. Every row of the table is a case here.
func TestPlanSLARematch(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3 * time.Hour)
	met := now.Add(-2 * time.Hour)
	frozen := int64(600)
	pending := now.Add(-10 * time.Minute)

	policyA, policyB := uuid.New(), uuid.New()
	tk := Ticket{ID: uuid.New(), SLAPausedSeconds: 600}
	pendingTk := Ticket{ID: tk.ID, SLAPausedSeconds: 600, PendingSince: &pending}

	running := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started, PausedSecondsAtStart: 120}
	runningFromCreation := &SLARecordState{ID: uuid.New(), PolicyID: policyA}
	facts := SLAFacts{
		FirstResponseAt: &met, ResponseElapsedAtMetSeconds: &frozen,
		ResponseBreachedAt: &met,
	}
	withFacts := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started, PausedSecondsAtStart: 120, Facts: facts}

	cases := []struct {
		name    string
		enabled bool
		cur     *SLARecordState
		matched *uuid.UUID
		tk      Ticket
		want    SLAOutcome
		// wantStart describes the new record; nil means none is started.
		wantStart *SLARecordStart
	}{
		{name: "switched off", enabled: false, cur: running, matched: &policyB, tk: tk, want: SLAOff},
		{name: "no record, no policy matches", enabled: true, tk: tk, want: SLANone},
		{
			// Erik's rule: the clock started when the ticket was created, not
			// when somebody changed the priority. It may breach at once.
			name: "no record, policy matches: the clock starts at ticket creation", enabled: true,
			matched: &policyB, tk: tk, want: SLAAttached,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: nil, PausedSecondsAtStart: 0},
		},
		{
			name: "no record, policy matches, ticket pending: still from creation", enabled: true,
			matched: &policyB, tk: pendingTk, want: SLAAttached,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: nil, PausedSecondsAtStart: 0},
		},
		{name: "same policy, running", enabled: true, cur: running, matched: &policyA, tk: tk, want: SLAUnchanged},
		{name: "same policy, targets met", enabled: true, cur: withFacts, matched: &policyA, tk: tk, want: SLAUnchanged},
		{
			name: "running record, other policy: the clock is inherited", enabled: true,
			cur: running, matched: &policyB, tk: tk, want: SLARematched,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: &started, PausedSecondsAtStart: 120},
		},
		{
			name: "running record from creation, other policy: still from creation", enabled: true,
			cur: runningFromCreation, matched: &policyB, tk: tk, want: SLARematched,
			wantStart: &SLARecordStart{TicketID: tk.ID, PolicyID: policyB, StartedAt: nil, PausedSecondsAtStart: 0},
		},
		{
			// The rule that replaced the restart: a met target stays met, and
			// the clock keeps its start.
			name: "record with targets met, other policy: re-matched, clock and facts carried over", enabled: true,
			cur: withFacts, matched: &policyB, tk: tk, want: SLARematched,
			wantStart: &SLARecordStart{
				TicketID: tk.ID, PolicyID: policyB, StartedAt: &started, PausedSecondsAtStart: 120, Facts: facts,
			},
		},
		{name: "running record, no policy matches", enabled: true, cur: running, tk: tk, want: SLARemoved},
		{name: "record with targets met, no policy matches", enabled: true, cur: withFacts, tk: tk, want: SLARemoved},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, start := planSLARematch(tc.enabled, tc.cur, tc.matched, tc.tk, now)
			require.Equal(t, tc.want, got)
			requireStart(t, tc.wantStart, start)
		})
	}
}

func requireStart(t *testing.T, want, got *SLARecordStart) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.NotEqual(t, uuid.Nil, got.ID, "the new record gets its own id")
	w := *want
	w.ID = got.ID
	require.Equal(t, w, *got)
}

// planSLARestart is the reopen path's effect (#464): the clocks start over from
// now, whatever the priority did, and whether or not the old record was met.
func TestPlanSLARestart(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3 * time.Hour)
	met := now.Add(-2 * time.Hour)
	pending := now.Add(-10 * time.Minute)

	policyA, policyB := uuid.New(), uuid.New()
	tk := Ticket{ID: uuid.New(), SLAPausedSeconds: 600}
	pendingTk := Ticket{ID: tk.ID, SLAPausedSeconds: 600, PendingSince: &pending}

	running := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started}
	settled := &SLARecordState{ID: uuid.New(), PolicyID: policyA, StartedAt: &started, Facts: SLAFacts{ResolvedAt: &met}}
	fresh := func(policy uuid.UUID, base int64) *SLARecordStart {
		return &SLARecordStart{TicketID: tk.ID, PolicyID: policy, StartedAt: &now, PausedSecondsAtStart: base}
	}

	cases := []struct {
		name      string
		enabled   bool
		cur       *SLARecordState
		matched   *uuid.UUID
		tk        Ticket
		want      SLAOutcome
		wantStart *SLARecordStart
	}{
		{name: "switched off: nothing is created", enabled: false, cur: settled, matched: &policyB, tk: tk, want: SLAOff},
		{name: "no record, no policy", enabled: true, tk: tk, want: SLANone},
		{name: "no record, a policy matches: a clock starts now", enabled: true, matched: &policyB, tk: tk, want: SLAAttached, wantStart: fresh(policyB, 600)},
		{name: "settled record, other policy", enabled: true, cur: settled, matched: &policyB, tk: tk, want: SLARestarted, wantStart: fresh(policyB, 600)},
		{name: "settled record, SAME policy: restarts anyway", enabled: true, cur: settled, matched: &policyA, tk: tk, want: SLARestarted, wantStart: fresh(policyA, 600)},
		{name: "running record, other policy: restarts too", enabled: true, cur: running, matched: &policyB, tk: tk, want: SLARestarted, wantStart: fresh(policyB, 600)},
		{name: "ticket pending: the base includes the open interval", enabled: true, cur: settled, matched: &policyB, tk: pendingTk, want: SLARestarted, wantStart: fresh(policyB, 1200)},
		{name: "no policy matches: the old record is superseded", enabled: true, cur: settled, tk: tk, want: SLARemoved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, start := planSLARestart(tc.enabled, tc.cur, tc.matched, tc.tk, now)
			require.Equal(t, tc.want, got)
			requireStart(t, tc.wantStart, start)
		})
	}
}
