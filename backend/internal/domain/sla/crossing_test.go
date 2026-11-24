package sla_test

import (
	"testing"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// crossPolicy is response 100 min / resolution 1000 min. With a ticket created
// at statusCreated and now = statusCreated + elapsed, the resolution clock stays
// far below its 80% line for every row below, so only the response column
// moves and the expectations name it alone.
var crossPolicy = sla.Policy{ResponseTargetMin: 100, ResolutionTargetMin: 1000}

func TestCrossings_BoundariesAndFreshness(t *testing.T) {
	cases := []struct {
		name    string
		elapsed time.Duration
		want    sla.Crossed
	}{
		{
			name:    "one second under 80%",
			elapsed: 80*time.Minute - time.Second,
			want:    sla.Crossed{},
		},
		{
			name:    "exactly 80% is at risk and fresh",
			elapsed: 80 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{Due: true, Fresh: true}, {}}},
		},
		{
			name:    "at risk exactly 15 minutes past 80% is still fresh",
			elapsed: 95 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{Due: true, Fresh: true}, {}}},
		},
		{
			name:    "at risk one second past the window is stale",
			elapsed: 95*time.Minute + time.Second,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{Due: true}, {}}},
		},
		{
			name:    "one second under 100%: at risk, never breach",
			elapsed: 100*time.Minute - time.Second,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{Due: true}, {}}},
		},
		{
			name:    "exactly 100%: breach and no at risk",
			elapsed: 100 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{Due: true, Fresh: true}, {}}},
		},
		{
			name:    "breach exactly 15 minutes past target is fresh",
			elapsed: 115 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{Due: true, Fresh: true}, {}}},
		},
		{
			name:    "breach one second past the window is stale",
			elapsed: 115*time.Minute + time.Second,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{Due: true}, {}}},
		},
		{
			name:    "far past target stays a stale breach, never at risk",
			elapsed: 150 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{Due: true}, {}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := ticket.Ticket{CreatedAt: statusCreated}
			now := statusCreated.Add(tc.elapsed)
			require.Equal(t, tc.want, sla.Crossings(sla.Record{}, crossPolicy, tk, now))
		})
	}
}

func TestCrossings_MetTargetNeverCrosses(t *testing.T) {
	tk := ticket.Ticket{CreatedAt: statusCreated}
	met := statusCreated.Add(10 * time.Minute)
	now := statusCreated.Add(150 * time.Minute)

	got := sla.Crossings(sla.Record{FirstResponseAt: &met}, crossPolicy, tk, now)
	require.Equal(t, sla.Crossing{}, got.Breach[0])
	require.Equal(t, sla.Crossing{}, got.AtRisk[0])
}

// Each target reads its own met flag: an answered ticket still has a live
// resolution clock, and a resolved ticket still owes (or has missed) its
// response. Resolution is 1000 min, so its 80% line is 800 and its 100% line
// is 1000.
func TestCrossings_ResolutionTarget(t *testing.T) {
	answered := statusCreated.Add(10 * time.Minute)
	resolved := statusCreated.Add(20 * time.Minute)
	fresh := sla.Crossing{Due: true, Fresh: true}
	stale := sla.Crossing{Due: true}

	cases := []struct {
		name    string
		rec     sla.Record
		elapsed time.Duration
		want    sla.Crossed
	}{
		{
			name:    "answered, resolution one second under 80%",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 800*time.Minute - time.Second,
			want:    sla.Crossed{},
		},
		{
			name:    "answered, resolution exactly 80% is at risk and fresh",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 800 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{}, fresh}},
		},
		{
			name:    "answered, resolution 15 minutes past 80% is still fresh",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 815 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{}, fresh}},
		},
		{
			name:    "answered, resolution one second past the at-risk window is stale",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 815*time.Minute + time.Second,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{}, stale}},
		},
		{
			name:    "answered, resolution one second under 100% is at risk, never breach",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 1000*time.Minute - time.Second,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{}, stale}},
		},
		{
			name:    "answered, resolution exactly 100% is a fresh breach and no at risk",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 1000 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{}, fresh}},
		},
		{
			name:    "answered, resolution breach 15 minutes past target is fresh",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 1015 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{}, fresh}},
		},
		{
			name:    "answered, resolution breach one second past the window is stale",
			rec:     sla.Record{FirstResponseAt: &answered},
			elapsed: 1015*time.Minute + time.Second,
			want:    sla.Crossed{Breach: [2]sla.Crossing{{}, stale}},
		},
		{
			name:    "not answered: stale response breach alongside fresh resolution at risk",
			rec:     sla.Record{},
			elapsed: 800 * time.Minute,
			want: sla.Crossed{
				Breach: [2]sla.Crossing{stale, {}},
				AtRisk: [2]sla.Crossing{{}, fresh},
			},
		},
		{
			name:    "resolved: resolution never crosses, unanswered response still breaches",
			rec:     sla.Record{ResolvedAt: &resolved},
			elapsed: 1000 * time.Minute,
			want:    sla.Crossed{Breach: [2]sla.Crossing{stale, {}}},
		},
		{
			name:    "resolved: unanswered response still goes at risk",
			rec:     sla.Record{ResolvedAt: &resolved},
			elapsed: 80 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
		{
			name:    "answered and resolved: nothing crosses",
			rec:     sla.Record{FirstResponseAt: &answered, ResolvedAt: &resolved},
			elapsed: 1000 * time.Minute,
			want:    sla.Crossed{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := ticket.Ticket{CreatedAt: statusCreated}
			now := statusCreated.Add(tc.elapsed)
			require.Equal(t, tc.want, sla.Crossings(tc.rec, crossPolicy, tk, now))
		})
	}
}

// Freshness is on the SLA clock, so pause never makes a crossing stale. The
// whole reading is compared, so a row also pins that no breach is reported.
func TestCrossings_FreshnessAcrossAPause(t *testing.T) {
	fresh := sla.Crossing{Due: true, Fresh: true}
	cases := []struct {
		name string
		tk   ticket.Ticket
		now  time.Time
		want sla.Crossed
	}{
		{
			name: "pause closed, 80% reached a minute later is fresh",
			// Paused for an hour: wall clock 140m, SLA clock 80m.
			tk:   ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: 3600},
			now:  statusCreated.Add(140 * time.Minute),
			want: sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
		{
			name: "wall clock far past the threshold but SLA clock inside the window",
			// Paused for 200 minutes: wall clock 280m, SLA clock 80m.
			tk:   ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: 200 * 60},
			now:  statusCreated.Add(280 * time.Minute),
			want: sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
		{
			name: "open pause freezes the clock below 80%",
			// Pending since 70m, now 200m: SLA clock stays at 70m.
			tk:   ticket.Ticket{CreatedAt: statusCreated, PendingSince: ptrTime(statusCreated.Add(70 * time.Minute))},
			now:  statusCreated.Add(200 * time.Minute),
			want: sla.Crossed{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.Crossings(sla.Record{}, crossPolicy, tc.tk, tc.now))
		})
	}
}

// Crossings reads the record's own clock, not the ticket's. A reopened ticket's
// restarted record starts counting at StartedAt, with the pause total as it
// stood then taken off. Reading the ticket-creation clock instead would see
// that record as long past target and stamp it silently as stale, with no
// alert. Response target is 100 min, so its 80% line is 80.
func TestCrossings_ReadsTheRecordsOwnClock(t *testing.T) {
	fresh := sla.Crossing{Due: true, Fresh: true}
	cases := []struct {
		name string
		tk   ticket.Ticket
		rec  sla.Record
		now  time.Time
		want sla.Crossed
	}{
		{
			// Ticket clock reads 500m, a stale response breach. Record clock
			// reads 80m, a fresh at-risk crossing.
			name: "restarted record counts from StartedAt, not from ticket creation",
			tk:   ticket.Ticket{CreatedAt: statusCreated},
			rec:  sla.Record{StartedAt: ptrTime(statusCreated.Add(420 * time.Minute))},
			now:  statusCreated.Add(500 * time.Minute),
			want: sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
		{
			// An hour was paused before the restart. Ticket clock reads 220m,
			// a stale breach. Counting from StartedAt but ignoring the pause
			// base would read 20m, nothing. The record clock reads 80m.
			name: "pause before the restart is excluded from the record's elapsed",
			tk:   ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: 3600},
			rec: sla.Record{
				StartedAt:            ptrTime(statusCreated.Add(200 * time.Minute)),
				PausedSecondsAtStart: 3600,
			},
			now:  statusCreated.Add(280 * time.Minute),
			want: sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
		{
			// The restarted record breaches on its own 100%, fresh, though the
			// ticket is far older.
			name: "restarted record breaches on its own clock",
			tk:   ticket.Ticket{CreatedAt: statusCreated},
			rec:  sla.Record{StartedAt: ptrTime(statusCreated.Add(400 * time.Minute))},
			now:  statusCreated.Add(500 * time.Minute),
			want: sla.Crossed{Breach: [2]sla.Crossing{fresh, {}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.Crossings(tc.rec, crossPolicy, tc.tk, tc.now))
		})
	}
}

// A target whose breach stamp is already set never crosses again. The stamp
// records that the breach happened, and an at-risk stamp is never written once
// the target is breached. This matters when a policy's target is loosened in
// place after the stamp: the clock can fall back under 100% (even under 80%)
// of the new target, and the reading must not report at risk, or a second
// breach, for a target that already breached. Response is 100 min, resolution
// 1000 min.
func TestCrossings_BreachStampedTargetNeverCrossesAgain(t *testing.T) {
	stamp := statusCreated.Add(100 * time.Minute)
	fresh := sla.Crossing{Due: true, Fresh: true}
	answered := statusCreated.Add(10 * time.Minute)

	cases := []struct {
		name    string
		rec     sla.Record
		elapsed time.Duration
		want    sla.Crossed
	}{
		{
			name:    "response stamped, clock in the at-risk band: no at risk",
			rec:     sla.Record{ResponseBreachedAt: &stamp},
			elapsed: 90 * time.Minute,
			want:    sla.Crossed{},
		},
		{
			name:    "response stamped, clock past the target: no new breach",
			rec:     sla.Record{ResponseBreachedAt: &stamp},
			elapsed: 100 * time.Minute,
			want:    sla.Crossed{},
		},
		{
			name:    "resolution stamped, clock in the at-risk band: no at risk",
			rec:     sla.Record{FirstResponseAt: &answered, ResolutionBreachedAt: &stamp},
			elapsed: 900 * time.Minute,
			want:    sla.Crossed{},
		},
		{
			name:    "resolution stamped, clock past the target: no new breach",
			rec:     sla.Record{FirstResponseAt: &answered, ResolutionBreachedAt: &stamp},
			elapsed: 1000 * time.Minute,
			want:    sla.Crossed{},
		},
		{
			name:    "response stamped does not silence resolution",
			rec:     sla.Record{ResponseBreachedAt: &stamp},
			elapsed: 800 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{{}, fresh}},
		},
		{
			name:    "resolution stamped does not silence response",
			rec:     sla.Record{ResolutionBreachedAt: &stamp},
			elapsed: 80 * time.Minute,
			want:    sla.Crossed{AtRisk: [2]sla.Crossing{fresh, {}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := ticket.Ticket{CreatedAt: statusCreated}
			now := statusCreated.Add(tc.elapsed)
			require.Equal(t, tc.want, sla.Crossings(tc.rec, crossPolicy, tk, now))
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestAnnounce(t *testing.T) {
	fresh := sla.Crossing{Due: true, Fresh: true}
	stale := sla.Crossing{Due: true}
	none := sla.Crossing{}
	both := [2]bool{true, true}
	resp := [2]bool{true, false}
	res := [2]bool{false, true}

	cases := []struct {
		name  string
		newly sla.Stamped
		c     sla.Crossed
		want  sla.Announcement
	}{
		{
			name:  "nothing newly stamped: a fresh crossing that was already stamped is not announced",
			newly: sla.Stamped{},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, none}},
			want:  sla.Announcement{},
		},
		{
			name:  "fresh breach of response",
			newly: sla.Stamped{Breach: resp},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, none}},
			want:  sla.Announcement{Event: sla.EventBreached, Targets: []sla.Target{sla.TargetResponse}},
		},
		{
			name:  "both fresh breaches are one event",
			newly: sla.Stamped{Breach: both},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, fresh}},
			want:  sla.Announcement{Event: sla.EventBreached, Targets: []sla.Target{sla.TargetResponse, sla.TargetResolution}},
		},
		{
			name:  "breach of one with fresh at risk of the other names it",
			newly: sla.Stamped{Breach: resp, AtRisk: res},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, none}, AtRisk: [2]sla.Crossing{none, fresh}},
			want: sla.Announcement{
				Event:         sla.EventBreached,
				Targets:       []sla.Target{sla.TargetResponse},
				AtRiskTargets: []sla.Target{sla.TargetResolution},
			},
		},
		{
			name:  "fresh at risk of one",
			newly: sla.Stamped{AtRisk: resp},
			c:     sla.Crossed{AtRisk: [2]sla.Crossing{fresh, none}},
			want:  sla.Announcement{Event: sla.EventAtRisk, Targets: []sla.Target{sla.TargetResponse}},
		},
		{
			name:  "both fresh at risk are one event",
			newly: sla.Stamped{AtRisk: both},
			c:     sla.Crossed{AtRisk: [2]sla.Crossing{fresh, fresh}},
			want:  sla.Announcement{Event: sla.EventAtRisk, Targets: []sla.Target{sla.TargetResponse, sla.TargetResolution}},
		},
		{
			name:  "stale breach with fresh at risk of the other raises at risk",
			newly: sla.Stamped{Breach: resp, AtRisk: res},
			c:     sla.Crossed{Breach: [2]sla.Crossing{stale, none}, AtRisk: [2]sla.Crossing{none, fresh}},
			want:  sla.Announcement{Event: sla.EventAtRisk, Targets: []sla.Target{sla.TargetResolution}, Stale: 1},
		},
		{
			name:  "fresh breach with stale at risk of the other is breached, at risk omitted and counted stale",
			newly: sla.Stamped{Breach: resp, AtRisk: res},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, none}, AtRisk: [2]sla.Crossing{none, stale}},
			want:  sla.Announcement{Event: sla.EventBreached, Targets: []sla.Target{sla.TargetResponse}, Stale: 1},
		},
		{
			name:  "stale breach alone is announced as nothing",
			newly: sla.Stamped{Breach: resp},
			c:     sla.Crossed{Breach: [2]sla.Crossing{stale, none}},
			want:  sla.Announcement{Stale: 1},
		},
		{
			name:  "stale at risk alone is announced as nothing",
			newly: sla.Stamped{AtRisk: resp},
			c:     sla.Crossed{AtRisk: [2]sla.Crossing{stale, none}},
			want:  sla.Announcement{Stale: 1},
		},
		{
			name:  "of two breaches, only the stale one is counted",
			newly: sla.Stamped{Breach: both},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, stale}},
			want:  sla.Announcement{Event: sla.EventBreached, Targets: []sla.Target{sla.TargetResponse}, Stale: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.Announce(tc.newly, tc.c))
		})
	}
}

// The at-risk stamp is the sweep's bookkeeping. The indicator never reads it,
// so a record carrying one reads exactly as a record without it.
func TestStatusFor_IgnoresAtRiskStamps(t *testing.T) {
	tk := ticket.Ticket{CreatedAt: statusCreated}
	stamp := statusCreated.Add(80 * time.Minute)
	for _, elapsed := range []time.Duration{50 * time.Minute, 85 * time.Minute, 120 * time.Minute} {
		now := statusCreated.Add(elapsed)
		bare := sla.StatusFor(sla.Record{}, statusPolicy, tk, now)
		stamped := sla.StatusFor(sla.Record{ResponseAtRiskAt: &stamp, ResolutionAtRiskAt: &stamp}, statusPolicy, tk, now)
		require.Equal(t, bare, stamped, "elapsed %s", elapsed)
	}
}
