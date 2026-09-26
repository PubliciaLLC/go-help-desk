package sla_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// statusPolicy is response 100 min / resolution 1000 min, so a percentage of
// either target is a round number and cases read as "elapsed X, expect Y"
// rather than repeating arithmetic.
var statusPolicy = sla.Policy{
	ID:                  uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	Name:                "Critical — 1h response",
	ResponseTargetMin:   100,
	ResolutionTargetMin: 1000,
}

var statusCreated = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

func TestStatusFor_ResponseColorBoundaries(t *testing.T) {
	cases := []struct {
		name           string
		elapsed        time.Duration
		wantColor      sla.Color
		wantRemaining  int
		wantElapsedMin int
	}{
		{name: "well under", elapsed: 50 * time.Minute, wantColor: sla.Green, wantRemaining: 50, wantElapsedMin: 50},
		{name: "one second under 80%", elapsed: 79*time.Minute + 59*time.Second, wantColor: sla.Green, wantRemaining: 20, wantElapsedMin: 79},
		{name: "exactly 80%", elapsed: 80 * time.Minute, wantColor: sla.Amber, wantRemaining: 20, wantElapsedMin: 80},
		{name: "one second under 100%", elapsed: 99*time.Minute + 59*time.Second, wantColor: sla.Amber, wantRemaining: 0, wantElapsedMin: 99},
		{name: "exactly 100%", elapsed: 100 * time.Minute, wantColor: sla.Red, wantRemaining: 0, wantElapsedMin: 100},
		{name: "past", elapsed: 150 * time.Minute, wantColor: sla.Red, wantRemaining: -50, wantElapsedMin: 150},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := ticket.Ticket{CreatedAt: statusCreated}
			now := statusCreated.Add(tc.elapsed)

			got := sla.StatusFor(sla.Record{}, statusPolicy, tk, now)

			require.Equal(t, tc.wantColor, got.Response.Color)
			require.Equal(t, tc.wantElapsedMin, got.Response.ElapsedMin)
			require.Equal(t, tc.wantRemaining, got.Response.RemainingMin)
			require.Nil(t, got.Response.MetAt)
		})
	}
}

func TestStatusFor_MetTargetsFreezeAtTheirOwnInstant(t *testing.T) {
	t.Run("response met early, resolution outstanding", func(t *testing.T) {
		metAt := statusCreated.Add(30 * time.Minute)
		now := statusCreated.Add(900 * time.Minute) // resolution: 900/1000 = 90%
		rec := sla.Record{FirstResponseAt: &metAt}
		tk := ticket.Ticket{CreatedAt: statusCreated}

		got := sla.StatusFor(rec, statusPolicy, tk, now)

		require.Equal(t, sla.Green, got.Response.Color, "frozen at 30/100 = 30%, not judged against now")
		require.Equal(t, 30, got.Response.ElapsedMin)
		require.NotNil(t, got.Response.MetAt)
		require.True(t, metAt.Equal(*got.Response.MetAt))

		require.Equal(t, sla.Amber, got.Resolution.Color)
		require.Nil(t, got.Resolution.MetAt)
	})

	t.Run("response met late stays red and frozen", func(t *testing.T) {
		metAt := statusCreated.Add(120 * time.Minute) // 120/100 = 120%, past target
		now := statusCreated.Add(121 * time.Minute)
		rec := sla.Record{FirstResponseAt: &metAt}
		tk := ticket.Ticket{CreatedAt: statusCreated}

		got := sla.StatusFor(rec, statusPolicy, tk, now)

		require.Equal(t, sla.Red, got.Response.Color)
		require.Equal(t, -20, got.Response.RemainingMin)
	})

	t.Run("resolved on time, checked much later, stays green", func(t *testing.T) {
		metAt := statusCreated.Add(500 * time.Minute) // 500/1000 = 50%
		now := statusCreated.Add(5000 * time.Minute)
		rec := sla.Record{ResolvedAt: &metAt}
		tk := ticket.Ticket{CreatedAt: statusCreated}

		got := sla.StatusFor(rec, statusPolicy, tk, now)

		require.Equal(t, sla.Green, got.Resolution.Color)
		require.Equal(t, 500, got.Resolution.ElapsedMin)
	})

	t.Run("both met are both frozen", func(t *testing.T) {
		respAt := statusCreated.Add(10 * time.Minute)
		resAt := statusCreated.Add(200 * time.Minute)
		rec := sla.Record{FirstResponseAt: &respAt, ResolvedAt: &resAt}
		tk := ticket.Ticket{CreatedAt: statusCreated}
		now := statusCreated.Add(100000 * time.Minute)

		got := sla.StatusFor(rec, statusPolicy, tk, now)

		require.NotNil(t, got.Response.MetAt)
		require.NotNil(t, got.Resolution.MetAt)
		require.Equal(t, 10, got.Response.ElapsedMin)
		require.Equal(t, 200, got.Resolution.ElapsedMin)
	})
}

// The regression for #184: once frozenSeconds is set, it is what gets read —
// never Elapsed(t, *metAt) recomputed against t. A ticket's SLAPausedSeconds
// only ever grows across its life, so recomputing against "today's" ticket
// would silently subtract pause time that had not happened yet when the
// target was met. Constructing a ticket whose live recompute would produce a
// completely different answer than the frozen number is the point: if
// targetStatus ever went back to recomputing, this test would catch it
// immediately, not just fail to notice by coincidence.
func TestTargetStatus_UsesFrozenElapsedSeconds_IgnoringTicketChanges(t *testing.T) {
	metAt := statusCreated.Add(120 * time.Minute) // 120/100 = 120%: a genuine breach
	frozen := int64(120 * 60)                     // frozen at that instant, in seconds
	rec := sla.Record{FirstResponseAt: &metAt, ResponseElapsedAtMetSeconds: &frozen}

	// If this were recomputed live against *metAt instead of read from
	// frozen, a SLAPausedSeconds accumulated well AFTER metAt would drag
	// elapsed back down from 120min to 70min (120-50) — 70%, green — and
	// repaint a genuine breach as healthy.
	tk := ticket.Ticket{CreatedAt: statusCreated, SLAPausedSeconds: int64(50 * time.Minute / time.Second)}
	now := statusCreated.Add(500 * time.Minute)

	got := sla.StatusFor(rec, statusPolicy, tk, now)

	require.Equal(t, 120, got.Response.ElapsedMin, "must read the frozen number, not recompute")
	require.Equal(t, sla.Red, got.Response.Color, "the late response must stay red however much the ticket pauses later")
}

// A record whose target was met before this column existed (frozenSeconds
// nil) falls back to the old live recompute — wrong in exactly the way the
// column exists to fix, but only for rows a migration backfill did not reach,
// and strictly no worse than today's behaviour for those rows.
func TestTargetStatus_FallsBackToLiveRecomputeWhenFrozenSecondsIsNil(t *testing.T) {
	metAt := statusCreated.Add(60 * time.Minute)
	rec := sla.Record{FirstResponseAt: &metAt} // no frozen column
	tk := ticket.Ticket{CreatedAt: statusCreated}
	now := statusCreated.Add(500 * time.Minute)

	got := sla.StatusFor(rec, statusPolicy, tk, now)

	require.Equal(t, 60, got.Response.ElapsedMin, "recomputed from CreatedAt to metAt with no pause activity")
}

// The color is a live read of elapsed-toward-target; the breach stamp is not
// consulted at all, so a response stamped breached but well under target time
// is still green. Pinned so nobody "optimises" the stamp back in and
// reintroduces the sweep-interval lag this type exists to avoid.
func TestStatusFor_IgnoresBreachStamps(t *testing.T) {
	breachedAt := statusCreated.Add(5 * time.Minute)
	rec := sla.Record{ResponseBreachedAt: &breachedAt}
	tk := ticket.Ticket{CreatedAt: statusCreated}
	now := statusCreated.Add(10 * time.Minute) // 10/100 = 10%, well under 80%

	got := sla.StatusFor(rec, statusPolicy, tk, now)

	require.Equal(t, sla.Green, got.Response.Color, "the breach stamp must not be consulted")
}

// Response and resolution are independent: one can be red while the other is
// still green, in the same Status.
func TestStatusFor_TargetsAreIndependent(t *testing.T) {
	tk := ticket.Ticket{CreatedAt: statusCreated}
	now := statusCreated.Add(150 * time.Minute) // response 150/100=150% red, resolution 150/1000=15% green

	got := sla.StatusFor(sla.Record{}, statusPolicy, tk, now)

	require.Equal(t, sla.Red, got.Response.Color)
	require.Equal(t, sla.Green, got.Resolution.Color)
}

func TestStatusFor_PolicyIdentityCarriesThrough(t *testing.T) {
	tk := ticket.Ticket{CreatedAt: statusCreated}
	got := sla.StatusFor(sla.Record{}, statusPolicy, tk, statusCreated)

	require.Equal(t, statusPolicy.ID, got.PolicyID)
	require.Equal(t, statusPolicy.Name, got.PolicyName)
}

// colorFor's boundary is elapsed*5 >= target*4, integer arithmetic against a
// target that does not divide evenly by 5 — pinning that the comparison, not
// a floating-point percentage, decides the color.
func TestColorFor_OddTargetBoundary(t *testing.T) {
	oddPolicy := sla.Policy{ResponseTargetMin: 7, ResolutionTargetMin: 7}
	tk := ticket.Ticket{CreatedAt: statusCreated}

	cases := []struct {
		name    string
		elapsed time.Duration
		want    sla.Color
	}{
		// 80% of 7 minutes is 5.6 minutes = 336s. elapsed*5 >= target*4 is
		// elapsed*5 >= 28min i.e. elapsed >= 336s.
		{name: "one second under the 80% boundary", elapsed: 335 * time.Second, want: sla.Green},
		{name: "exactly at the 80% boundary", elapsed: 336 * time.Second, want: sla.Amber},
		{name: "one second under target", elapsed: 7*time.Minute - time.Second, want: sla.Amber},
		{name: "exactly at target", elapsed: 7 * time.Minute, want: sla.Red},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := statusCreated.Add(tc.elapsed)
			got := sla.StatusFor(sla.Record{}, oddPolicy, tk, now)
			require.Equal(t, tc.want, got.Response.Color)
		})
	}
}
