package sla_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
)

// EarlierRecord is what the ticket page's "SLA restarted" badge links to (#443,
// Erik 2026-10-14): the record a RESTART replaced, and only a restart. A restart
// is a reopen: it gives the new record a started_at of its own. A staff priority
// change re-matches instead, copying the old record's started_at and its met and
// breach stamps onto the new one, so it is never reported, however many times it
// happens and however much the old record had already recorded.
func TestEarlierRecord(t *testing.T) {
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(h int) *time.Time { v := created.Add(time.Duration(h) * time.Hour); return &v }
	pol := uuid.New()

	rec := func(over func(*sla.Record)) sla.Record {
		r := sla.Record{ID: uuid.New(), PolicyID: pol}
		over(&r)
		return r
	}
	none := func(*sla.Record) {}
	// A record a reopen started: its own started_at, no facts.
	restartedAt := func(h int) sla.Record { return rec(func(r *sla.Record) { r.StartedAt = at(h) }) }
	// A record a staff re-match replaced, as the store holds it.
	superseded := func(over func(*sla.Record)) sla.Record {
		return rec(func(r *sla.Record) { r.SupersededAt = at(5); over(r) })
	}

	cases := []struct {
		name        string
		history     []sla.Record // newest clock first, as the store returns it
		want        bool
		wantOutcome sla.EarlierOutcome
		wantStart   time.Time
	}{
		{name: "empty history", history: nil},
		{name: "no earlier record", history: []sla.Record{rec(none)}},
		{
			name: "a re-match of a met record is not a restart",
			history: []sla.Record{
				rec(func(r *sla.Record) { r.FirstResponseAt = at(2) }),
				superseded(func(r *sla.Record) { r.FirstResponseAt = at(2) }),
			},
		},
		{
			name: "a re-match of a breached record is not a restart",
			history: []sla.Record{
				rec(func(r *sla.Record) { r.ResolutionBreachedAt = at(4) }),
				superseded(func(r *sla.Record) { r.ResolutionBreachedAt = at(4) }),
			},
		},
		{
			name: "re-match after re-match stays absent, stamps copied down the chain",
			history: []sla.Record{
				rec(func(r *sla.Record) { r.FirstResponseAt, r.ResponseBreachedAt = at(2), at(3) }),
				superseded(func(r *sla.Record) { r.FirstResponseAt, r.ResponseBreachedAt = at(2), at(3) }),
				superseded(func(r *sla.Record) { r.FirstResponseAt, r.ResponseBreachedAt = at(2), at(3) }),
			},
		},
		{
			name: "a re-match of a record that already restarted carries the same clock, nothing new restarted",
			history: []sla.Record{
				rec(func(r *sla.Record) { r.StartedAt = at(6) }),
				superseded(func(r *sla.Record) { r.StartedAt = at(6) }),
			},
		},
		{
			name: "a restart whose predecessor was met",
			history: []sla.Record{
				restartedAt(6),
				superseded(func(r *sla.Record) { r.FirstResponseAt, r.ResolvedAt = at(1), at(3) }),
			},
			want:        true,
			wantOutcome: sla.EarlierMet,
			wantStart:   created, // nil started_at is the ticket's creation
		},
		{
			name: "a restart whose predecessor was breached",
			history: []sla.Record{
				restartedAt(6),
				superseded(func(r *sla.Record) { r.FirstResponseAt, r.ResolutionBreachedAt = at(1), at(4) }),
			},
			want:        true,
			wantOutcome: sla.EarlierBreached,
			wantStart:   created,
		},
		{
			name: "a restart of a record that had itself restarted reports that record's own start",
			history: []sla.Record{
				restartedAt(8),
				superseded(func(r *sla.Record) { r.StartedAt, r.ResponseBreachedAt = at(2), at(4) }),
			},
			want:        true,
			wantOutcome: sla.EarlierBreached,
			wantStart:   *at(2),
		},
		{
			name: "a restart, then a re-match: still reported, as the record the restart replaced",
			history: []sla.Record{
				rec(func(r *sla.Record) { r.StartedAt = at(6) }),
				superseded(func(r *sla.Record) { r.StartedAt = at(6) }),
				superseded(func(r *sla.Record) { r.ResolvedAt = at(3) }),
			},
			want:        true,
			wantOutcome: sla.EarlierMet,
			wantStart:   created,
		},
		{
			name: "a restart over a clock that was re-matched first reports the last record of that clock",
			history: []sla.Record{
				restartedAt(8),
				superseded(func(r *sla.Record) { r.ResolvedAt = at(3); r.ResponseBreachedAt = at(2) }),
				superseded(func(r *sla.Record) { r.ResponseBreachedAt = at(2) }),
			},
			want:        true,
			wantOutcome: sla.EarlierBreached,
			wantStart:   created,
		},
		{
			name: "a restart over a record with nothing met and nothing breached has no outcome to show",
			history: []sla.Record{
				restartedAt(6),
				superseded(none),
			},
		},
		{
			name: "a restart with nothing before it (a reopen that attached the first record)",
			history: []sla.Record{
				restartedAt(6),
			},
		},
		{
			name: "no current record: nothing to say",
			history: []sla.Record{
				superseded(func(r *sla.Record) { r.StartedAt, r.FirstResponseAt = at(2), at(3) }),
			},
		},
		{
			name: "a staff attach after a restart was removed starts from creation: not a restart",
			history: []sla.Record{
				superseded(func(r *sla.Record) { r.StartedAt, r.FirstResponseAt = at(2), at(3) }),
				rec(none),
			},
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
