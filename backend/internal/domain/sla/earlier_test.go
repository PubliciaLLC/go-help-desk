package sla_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
)

// EarlierRecord is what the ticket page's "SLA restarted" badge links to (#443,
// Erik 2026-10-13): the most recent record a priority change superseded, but
// only when that record's SLA had already been met or breached. A record
// superseded while still running is the same clock continuing, not a restart.
func TestEarlierRecord(t *testing.T) {
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(h int) *time.Time { v := created.Add(time.Duration(h) * time.Hour); return &v }
	pol := uuid.New()

	current := sla.Record{ID: uuid.New(), PolicyID: uuid.New()}
	superseded := func(over func(*sla.Record)) sla.Record {
		r := sla.Record{ID: uuid.New(), PolicyID: pol, SupersededAt: at(5)}
		over(&r)
		return r
	}
	none := func(*sla.Record) {}

	cases := []struct {
		name        string
		history     []sla.Record // newest clock first, as the store returns it
		want        bool
		wantOutcome sla.EarlierOutcome
		wantStart   time.Time
	}{
		{name: "no earlier record", history: []sla.Record{current}},
		{name: "empty history", history: nil},
		{
			name:    "superseded while running is not a restart",
			history: []sla.Record{current, superseded(none)},
		},
		{
			name:        "response met",
			history:     []sla.Record{current, superseded(func(r *sla.Record) { r.FirstResponseAt = at(2) })},
			want:        true,
			wantOutcome: sla.EarlierMet,
			wantStart:   created, // nil started_at is the ticket's creation
		},
		{
			name: "resolution met",
			history: []sla.Record{current, superseded(func(r *sla.Record) {
				r.FirstResponseAt, r.ResolvedAt = at(1), at(3)
			})},
			want:        true,
			wantOutcome: sla.EarlierMet,
			wantStart:   created,
		},
		{
			name: "a breach wins over a met target",
			history: []sla.Record{current, superseded(func(r *sla.Record) {
				r.FirstResponseAt, r.ResolutionBreachedAt = at(1), at(4)
			})},
			want:        true,
			wantOutcome: sla.EarlierBreached,
			wantStart:   created,
		},
		{
			name: "a record that started over reports its own start",
			history: []sla.Record{current, superseded(func(r *sla.Record) {
				r.StartedAt, r.ResponseBreachedAt = at(2), at(4)
			})},
			want:        true,
			wantOutcome: sla.EarlierBreached,
			wantStart:   *at(2),
		},
		{
			name: "the newest superseded record is the one reported",
			history: []sla.Record{
				current,
				superseded(func(r *sla.Record) { r.StartedAt, r.FirstResponseAt = at(2), at(3) }),
				superseded(func(r *sla.Record) { r.ResponseBreachedAt = at(1) }),
			},
			want:        true,
			wantOutcome: sla.EarlierMet,
			wantStart:   *at(2),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sla.EarlierRecord(tc.history, created)
			if !tc.want {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.wantOutcome, got.Outcome)
			require.Equal(t, tc.wantStart, got.StartedAt)
			require.Equal(t, *at(5), got.SupersededAt)
			require.Equal(t, pol, got.PolicyID)
		})
	}
}
