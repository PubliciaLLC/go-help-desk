package sla_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// The wire format is a contract with the frontend, and it was wrong for the
// entire life of this type: no struct tags meant Go marshalled the field names
// verbatim — {"ID":…,"CategoryID":…} — while types.ts reads id/category_id. All
// six fields disagreed, so the admin SLA table rendered undefined for every
// one. It went unnoticed because SLA is behind a flag that defaults off.
//
// Asserting the exact JSON rather than round-tripping through the same struct:
// a round trip passes no matter what the names are, which is precisely how this
// stayed invisible.
func TestPolicy_JSONContract(t *testing.T) {
	priority := ticket.PriorityHigh
	categoryID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := sla.Policy{
		ID:                  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Name:                "Gold",
		Priority:            &priority,
		CategoryID:          &categoryID,
		ResponseTargetMin:   30,
		ResolutionTargetMin: 240,
	}

	b, err := json.Marshal(p)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, map[string]any{
		"id":                    "22222222-2222-2222-2222-222222222222",
		"name":                  "Gold",
		"priority":              "high",
		"category_id":           "11111111-1111-1111-1111-111111111111",
		"response_target_min":   float64(30),
		"resolution_target_min": float64(240),
	}, got, "the JSON the frontend reads must not drift from these names")
}

// A catch-all policy has neither priority nor category. Both are omitempty, so
// the keys are absent rather than null — which is what lets the UI show "Any
// priority" instead of an empty badge.
func TestPolicy_JSONOmitsAnyPriorityAndCategory(t *testing.T) {
	b, err := json.Marshal(sla.Policy{Name: "Catch-all", ResponseTargetMin: 60, ResolutionTargetMin: 480})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.NotContains(t, got, "priority", "a catch-all names no priority")
	require.NotContains(t, got, "category_id")
	require.Equal(t, "Catch-all", got["name"])
}

func TestRecord_JSONContract(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	b, err := json.Marshal(sla.Record{
		TicketID:        uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		PolicyID:        uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		FirstResponseAt: &at,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, "33333333-3333-3333-3333-333333333333", got["ticket_id"])
	require.Equal(t, "44444444-4444-4444-4444-444444444444", got["policy_id"])
	require.Contains(t, got, "first_response_at")
	require.NotContains(t, got, "resolved_at", "unset timestamps are absent, not null")
}

// ── Timer mechanics (pause / resume) ────────────────────────────────────────
//
// See docs/DESIGN.md, "SLA Tracking (v1)" → "Timer Mechanics (pause / resume)",
// and the design comment on #181: a target is measured against elapsed time
// since creation, MINUS time spent Pending, with the still-open interval added
// back in while the ticket is currently paused.

// pendingCreated is the fixed instant every Elapsed/breach case below measures
// from, so cases read as offsets from creation rather than repeating
// time.Now() arithmetic that would make failures hard to reason about.
var pendingCreated = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

func TestElapsed(t *testing.T) {
	min := time.Minute

	cases := []struct {
		name string
		tk   ticket.Ticket
		at   time.Time
		want time.Duration
	}{
		{
			name: "no pauses",
			tk:   ticket.Ticket{CreatedAt: pendingCreated},
			at:   pendingCreated.Add(90 * min),
			want: 90 * min,
		},
		{
			name: "one closed interval",
			tk:   ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((30 * min).Seconds())},
			at:   pendingCreated.Add(90 * min),
			want: 60 * min,
		},
		{
			name: "two closed intervals summed",
			tk:   ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((45 * min).Seconds())},
			at:   pendingCreated.Add(90 * min),
			want: 45 * min,
		},
		{
			name: "currently pending",
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(30 * min)),
			},
			at:   pendingCreated.Add(90 * min),
			want: 30 * min,
		},
		{
			name: "currently pending on top of a closed interval",
			tk: ticket.Ticket{
				CreatedAt:        pendingCreated,
				SLAPausedSeconds: int64((10 * min).Seconds()),
				PendingSince:     timePtr(pendingCreated.Add(30 * min)),
			},
			at:   pendingCreated.Add(90 * min),
			want: 20 * min,
		},
		{
			// The open interval started AFTER at: a reading taken as of an
			// earlier instant must not subtract pause time that had not
			// happened yet.
			name: "open interval started after at is clipped away",
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(80 * min)),
			},
			at:   pendingCreated.Add(60 * min),
			want: 60 * min,
		},
		{
			name: "at equals created",
			tk:   ticket.Ticket{CreatedAt: pendingCreated},
			at:   pendingCreated,
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.Elapsed(tc.tk, tc.at))
		})
	}
}

func TestIsResponseBreached(t *testing.T) {
	min := time.Minute
	policy := sla.Policy{ResponseTargetMin: 60}
	responded := pendingCreated.Add(10 * min)

	cases := []struct {
		name   string
		record sla.Record
		tk     ticket.Ticket
		now    time.Time
		want   bool
	}{
		{
			name: "no pause, over target",
			tk:   ticket.Ticket{CreatedAt: pendingCreated},
			now:  pendingCreated.Add(61 * min),
			want: true,
		},
		{
			name: "no pause, under target",
			tk:   ticket.Ticket{CreatedAt: pendingCreated},
			now:  pendingCreated.Add(59 * min),
			want: false,
		},
		{
			name: "wall clock over target but a closed pause brings it under",
			tk:   ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((5 * min).Seconds())},
			now:  pendingCreated.Add(61 * min),
			want: false,
		},
		{
			name: "currently pending keeps it under target",
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(30 * min)),
			},
			now:  pendingCreated.Add(61 * min),
			want: false,
		},
		{
			// Already breached BEFORE the ticket went pending: the pause
			// freezes elapsed time, it does not undo a breach that already
			// happened.
			name: "already breached before pending started",
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(90 * min)),
			},
			now:  pendingCreated.Add(121 * min),
			want: true,
		},
		{
			name:   "already responded is never a breach regardless of elapsed",
			record: sla.Record{FirstResponseAt: &responded},
			tk:     ticket.Ticket{CreatedAt: pendingCreated},
			now:    pendingCreated.Add(10 * time.Hour),
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.IsResponseBreached(tc.record, policy, tc.tk, tc.now))
		})
	}
}

func TestIsResolutionBreached(t *testing.T) {
	min := time.Minute
	policy := sla.Policy{ResolutionTargetMin: 60}

	cases := []struct {
		name   string
		record sla.Record
		tk     ticket.Ticket
		now    time.Time
		want   bool
	}{
		{
			name: "unresolved, no pause, over target",
			tk:   ticket.Ticket{CreatedAt: pendingCreated},
			now:  pendingCreated.Add(61 * min),
			want: true,
		},
		{
			name: "unresolved, a closed pause brings it under target",
			tk:   ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((5 * min).Seconds())},
			now:  pendingCreated.Add(61 * min),
			want: false,
		},
		{
			name: "unresolved and currently pending",
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(30 * min)),
			},
			now:  pendingCreated.Add(120 * min),
			want: false,
		},
		{
			name:   "resolved on time once the pause before it is counted",
			record: sla.Record{ResolvedAt: timePtr(pendingCreated.Add(70 * min))},
			tk:     ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((15 * min).Seconds())},
			now:    pendingCreated.Add(10 * time.Hour),
			want:   false,
		},
		{
			name:   "resolved late with no pause to explain it",
			record: sla.Record{ResolvedAt: timePtr(pendingCreated.Add(70 * min))},
			tk:     ticket.Ticket{CreatedAt: pendingCreated},
			now:    pendingCreated.Add(10 * time.Hour),
			want:   true,
		},
		{
			// The ticket went pending again AFTER it resolved. Elapsed is
			// judged as of the resolution instant, so a pause that started
			// later must not retroactively rescue an already-late
			// resolution.
			name:   "a later pause does not rescue an already-late resolution",
			record: sla.Record{ResolvedAt: timePtr(pendingCreated.Add(70 * min))},
			tk: ticket.Ticket{
				CreatedAt:    pendingCreated,
				PendingSince: timePtr(pendingCreated.Add(100 * min)),
			},
			now:  pendingCreated.Add(10 * time.Hour),
			want: true,
		},
		{
			// #218: same shape as the case above, but this time the PAUSE
			// has already CLOSED and grown SLAPausedSeconds by the time this
			// is checked (not merely started, as above) — the ticket
			// resolved late, was reopened, paused for a long interval, and
			// released again. A live Elapsed(t, *ResolvedAt) recompute
			// against the ticket's now-larger SLAPausedSeconds would shrink
			// 70min down to 40min — under the 60min target — flipping a
			// true breach into a false negative. The frozen
			// ResolutionElapsedAtMetSeconds this test sets must be preferred
			// instead, so the answer still agrees with the frozen indicator.
			name: "a later CLOSED pause does not rescue an already-late resolution when frozen elapsed is known",
			record: sla.Record{
				ResolvedAt:                    timePtr(pendingCreated.Add(70 * min)),
				ResolutionElapsedAtMetSeconds: int64Ptr(int64((70 * min).Seconds())),
			},
			tk: ticket.Ticket{
				CreatedAt:        pendingCreated,
				SLAPausedSeconds: int64((30 * min).Seconds()), // grown AFTER resolution
			},
			now:  pendingCreated.Add(10 * time.Hour),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.IsResolutionBreached(tc.record, policy, tc.tk, tc.now))
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
func int64Ptr(n int64) *int64        { return &n }

// ── Restarted records (#443b) ────────────────────────────────────────────────
//
// A record written by a priority re-match carries its own clock start and the
// pause total as it stood then. ElapsedFrom is Elapsed with both supplied;
// Record.Elapsed reads them off the record.

func TestElapsedFrom(t *testing.T) {
	min := time.Minute
	x := pendingCreated.Add(60 * min) // the restart instant

	cases := []struct {
		name       string
		start      time.Time
		pausedBase int64
		tk         ticket.Ticket
		at         time.Time
		want       time.Duration
	}{
		{
			name:  "restarted at X is zero at X",
			start: x,
			tk:    ticket.Ticket{CreatedAt: pendingCreated},
			at:    x,
			want:  0,
		},
		{
			name:  "ten minutes after the restart",
			start: x,
			tk:    ticket.Ticket{CreatedAt: pendingCreated},
			at:    x.Add(10 * min),
			want:  10 * min,
		},
		{
			// Pause time before the restart does not count toward the new
			// record: the base cancels the ticket's whole earlier total.
			name:       "pause total before the restart is cancelled by the base",
			start:      x,
			pausedBase: int64((20 * min).Seconds()),
			tk:         ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((20 * min).Seconds())},
			at:         x.Add(10 * min),
			want:       10 * min,
		},
		{
			// Pending opened before the restart and still open: the base
			// holds the part of it that had elapsed by x, and the open
			// interval is clipped to at, so the record has not run at all.
			name:       "pending opened before the restart and still open",
			start:      x,
			pausedBase: int64((30 * min).Seconds()), // pending since x-30m
			tk:         ticket.Ticket{CreatedAt: pendingCreated, PendingSince: timePtr(x.Add(-30 * min))},
			at:         x.Add(15 * min),
			want:       0,
		},
		{
			// Pending opened before the restart and closed 5m after it: the
			// ticket's total grows by the whole interval (35m), the base
			// already holds 30m of it, so 5m comes off the new record.
			name:       "pending opened before the restart and closed after it",
			start:      x,
			pausedBase: int64((30 * min).Seconds()),
			tk:         ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((35 * min).Seconds())},
			at:         x.Add(20 * min),
			want:       15 * min, // 20m - 5m
		},
		{
			name:  "pending opened after the restart behaves as before",
			start: x,
			tk:    ticket.Ticket{CreatedAt: pendingCreated, PendingSince: timePtr(x.Add(5 * min))},
			at:    x.Add(20 * min),
			want:  5 * min,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sla.ElapsedFrom(tc.start, tc.pausedBase, tc.tk, tc.at))
		})
	}
}

// Record.Elapsed reads the clock start and pause base off the record. A record
// with no StartedAt reads exactly as the package-level Elapsed does, which is
// what keeps every record written before 000041 behaving as it always did.
func TestRecord_Elapsed(t *testing.T) {
	min := time.Minute
	tk := ticket.Ticket{CreatedAt: pendingCreated, SLAPausedSeconds: int64((10 * min).Seconds())}
	at := pendingCreated.Add(90 * min)

	t.Run("nil StartedAt equals Elapsed", func(t *testing.T) {
		r := sla.Record{}
		require.Equal(t, sla.Elapsed(tk, at), r.Elapsed(tk, at))
		require.Equal(t, 80*min, r.Elapsed(tk, at))
	})

	t.Run("StartedAt and PausedSecondsAtStart are used", func(t *testing.T) {
		start := pendingCreated.Add(60 * min)
		r := sla.Record{StartedAt: &start, PausedSecondsAtStart: int64((10 * min).Seconds())}
		require.Equal(t, 30*min, r.Elapsed(tk, at))
	})
}

// A restarted record is measured from its own start, not from the ticket's
// creation. Without this the record a priority change starts on a ten-hour-old
// ticket would breach at once.
func TestIsBreached_RestartedRecordMeasuresFromItsOwnStart(t *testing.T) {
	now := pendingCreated.Add(10 * time.Hour)
	start := now.Add(-1 * time.Minute)
	tk := ticket.Ticket{CreatedAt: pendingCreated}
	p := sla.Policy{ResponseTargetMin: 5, ResolutionTargetMin: 5}

	restarted := sla.Record{StartedAt: &start}
	original := sla.Record{}

	require.False(t, sla.IsResponseBreached(restarted, p, tk, now))
	require.False(t, sla.IsResolutionBreached(restarted, p, tk, now))

	// The same ticket under a record that starts at creation is long breached.
	require.True(t, sla.IsResponseBreached(original, p, tk, now))
	require.True(t, sla.IsResolutionBreached(original, p, tk, now))

	// A resolved record with no frozen number falls back to a live recompute,
	// which must also use the record's own start.
	resolvedAt := now
	restarted.ResolvedAt = &resolvedAt
	require.False(t, sla.IsResolutionBreached(restarted, p, tk, now.Add(time.Hour)))
}
