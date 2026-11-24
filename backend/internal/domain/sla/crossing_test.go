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

// Freshness is on the SLA clock, so pause never makes a crossing stale.
func TestCrossings_FreshnessAcrossAPause(t *testing.T) {
	cases := []struct {
		name      string
		tk        ticket.Ticket
		now       time.Time
		wantDue   bool
		wantFresh bool
	}{
		{
			name: "pause closed, 80% reached a minute later is fresh",
			// Paused for an hour: wall clock 140m, SLA clock 80m.
			tk:        ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: 3600},
			now:       statusCreated.Add(140 * time.Minute),
			wantDue:   true,
			wantFresh: true,
		},
		{
			name: "wall clock far past the threshold but SLA clock inside the window",
			// Paused for 200 minutes: wall clock 280m, SLA clock 80m.
			tk:        ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: 200 * 60},
			now:       statusCreated.Add(280 * time.Minute),
			wantDue:   true,
			wantFresh: true,
		},
		{
			name: "open pause freezes the clock below 80%",
			// Pending since 70m, now 200m: SLA clock stays at 70m.
			tk:      ticket.Ticket{CreatedAt: statusCreated, PendingSince: ptrTime(statusCreated.Add(70 * time.Minute))},
			now:     statusCreated.Add(200 * time.Minute),
			wantDue: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sla.Crossings(sla.Record{}, crossPolicy, tc.tk, tc.now)
			require.Equal(t, tc.wantDue, got.AtRisk[0].Due)
			require.Equal(t, tc.wantFresh, got.AtRisk[0].Fresh)
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
			name:  "nothing newly stamped",
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
		{
			name:  "a fresh crossing that was already stamped is not announced",
			newly: sla.Stamped{},
			c:     sla.Crossed{Breach: [2]sla.Crossing{fresh, none}},
			want:  sla.Announcement{},
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
