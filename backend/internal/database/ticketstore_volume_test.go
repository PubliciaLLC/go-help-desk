package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TicketVolumeByDay (#675) counts tickets created and resolved per local day for
// the 30 days ending today. These tests run it against Postgres because the
// work is done by AT TIME ZONE, date_trunc and generate_series, which a fake
// would only restate.

// done sets how a ticket's resolution looks: the two columns the trend reads.
func (f *searchFixture) done(tk ticket.Ticket, resolved, closed *time.Time) {
	f.t.Helper()
	_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET resolved_at = $2, closed_at = $3 WHERE id = $1`,
		tk.ID, resolved, closed)
	require.NoError(f.t, err)
}

func (f *searchFixture) volume(fl ticket.Filter, tz string, now time.Time) []ticket.DayVolume {
	f.t.Helper()
	got, err := f.ts.VolumeByDay(f.ctx, fl, tz, now)
	require.NoError(f.t, err)
	return got
}

// at is the instant of a local wall-clock time in a named zone.
func at(t *testing.T, tz string, y int, m time.Month, d, h, min int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	require.NoError(t, err)
	return time.Date(y, m, d, h, min, 0, 0, loc)
}

func byDay(vs []ticket.DayVolume) map[string]ticket.DayVolume {
	out := map[string]ticket.DayVolume{}
	for _, v := range vs {
		out[v.Day] = v
	}
	return out
}

func TestVolumeByDay_ThirtyDaysOldestFirstWithZeros(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	f.mk(spec{subject: "v today", created: now.Add(-time.Hour)})
	f.mk(spec{subject: "v today 2", created: now.Add(-2 * time.Hour)})
	f.mk(spec{subject: "v first day", created: time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)})
	f.mk(spec{subject: "v day before the window", created: time.Date(2026, 5, 16, 23, 59, 59, 0, time.UTC)})

	got := f.volume(f.all(), "UTC", now)
	require.Len(t, got, 30)
	require.Equal(t, "2026-05-17", got[0].Day, "oldest first")
	require.Equal(t, "2026-06-15", got[29].Day, "ending today")
	for i := 1; i < len(got); i++ {
		a, _ := time.Parse("2006-01-02", got[i-1].Day)
		b, _ := time.Parse("2006-01-02", got[i].Day)
		require.Equal(t, a.AddDate(0, 0, 1), b, "consecutive days, none missing")
	}
	require.EqualValues(t, 1, got[0].Created, "the first local midnight is in the window")
	require.EqualValues(t, 2, got[29].Created)
	var total, resolved int64
	for _, d := range got {
		total += d.Created
		resolved += d.Resolved
	}
	require.EqualValues(t, 3, total, "the day before the window is not counted")
	require.Zero(t, resolved)
	require.Zero(t, got[10].Created+got[10].Resolved, "an empty day is present, as zeros")
}

func TestVolumeByDay_CountsLocalDays(t *testing.T) {
	// Today in each zone is 2 November 2026, the day after New York's clocks went
	// back, so the window crosses the 25-hour day. London changed a week before.
	cases := []struct {
		name string
		tz   string
		at   time.Time
		want string
	}{
		{"London, 23:30 in summer time", "Europe/London", at(t, "Europe/London", 2026, 10, 20, 23, 30), "2026-10-20"},
		{"London, 00:30 in summer time is the day before in UTC", "Europe/London", at(t, "Europe/London", 2026, 10, 21, 0, 30), "2026-10-21"},
		{"London, 23:30 on the day summer time ended", "Europe/London", at(t, "Europe/London", 2026, 10, 25, 23, 30), "2026-10-25"},
		{"New York, 23:30 the evening before the clocks go back", "America/New_York", at(t, "America/New_York", 2026, 10, 31, 23, 30), "2026-10-31"},
		{"New York, 23:30 on the 25-hour day", "America/New_York", at(t, "America/New_York", 2026, 11, 1, 23, 30), "2026-11-01"},
		{"New York, 00:30 on the 25-hour day", "America/New_York", at(t, "America/New_York", 2026, 11, 1, 0, 30), "2026-11-01"},
		{"UTC, the same instants are different days", "UTC", time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC), "2026-11-01"},
	}
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSearchFixture(t)
			tk := f.mk(spec{subject: "local", created: tc.at})
			f.done(tk, ref(tc.at), nil)

			got := f.volume(f.all(), tc.tz, now)
			require.Len(t, got, 30)
			require.Equal(t, "2026-11-02", got[29].Day)
			by := byDay(got)
			require.EqualValues(t, 1, by[tc.want].Created, "created on %s", tc.want)
			require.EqualValues(t, 1, by[tc.want].Resolved, "resolved on %s", tc.want)
			var c, r int64
			for _, d := range got {
				c, r = c+d.Created, r+d.Resolved
			}
			require.EqualValues(t, 1, c, "counted once, on one day")
			require.EqualValues(t, 1, r)
		})
	}

	t.Run("a day is a calendar day across the change to winter time", func(t *testing.T) {
		f := newSearchFixture(t)
		got := f.volume(f.all(), "America/New_York", now)
		require.Len(t, got, 30)
		require.Equal(t, "2026-10-04", got[0].Day)
		require.Equal(t, "2026-11-02", got[29].Day)
		days := map[string]bool{}
		for _, d := range got {
			require.False(t, days[d.Day], "%s twice", d.Day)
			days[d.Day] = true
		}
		require.True(t, days["2026-11-01"], "the 25-hour day is one row")
	})

	t.Run("today is today where the viewer is", func(t *testing.T) {
		f := newSearchFixture(t)
		late := time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC) // still 1 November in New York
		require.Equal(t, "2026-11-02", f.volume(f.all(), "UTC", late)[29].Day)
		require.Equal(t, "2026-11-01", f.volume(f.all(), "America/New_York", late)[29].Day)
	})

	t.Run("the window starts at the local midnight, to the instant", func(t *testing.T) {
		f := newSearchFixture(t)
		first := at(t, "America/New_York", 2026, 10, 4, 0, 0)
		f.mk(spec{subject: "on the boundary", created: first})
		f.mk(spec{subject: "just before", created: first.Add(-time.Microsecond)})
		got := f.volume(f.all(), "America/New_York", now)
		require.EqualValues(t, 1, got[0].Created)
	})
}

func TestVolumeByDay_ResolvedIsTheCurrentResolution(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	d := func(day int, h int) *time.Time { return ref(time.Date(2026, 6, day, h, 0, 0, 0, time.UTC)) }
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // created long before the window

	mk := func(name string, resolved, closed *time.Time) {
		f.done(f.mk(spec{subject: name, created: old}), resolved, closed)
	}
	mk("resolved", d(10, 9), nil)
	mk("closed without being resolved", nil, d(11, 9))
	mk("resolved then closed counts on the day it was resolved", d(12, 9), d(14, 9))
	mk("reopened: resolved_at cleared, closed_at cleared", nil, nil)
	mk("resolved before the window", ref(time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)), nil)
	mk("resolved twice on one day", d(10, 20), nil)

	by := byDay(f.volume(f.all(), "UTC", now))
	require.EqualValues(t, 2, by["2026-06-10"].Resolved)
	require.EqualValues(t, 1, by["2026-06-11"].Resolved, "through closed_at when resolved_at is null")
	require.EqualValues(t, 1, by["2026-06-12"].Resolved)
	require.Zero(t, by["2026-06-14"].Resolved, "a later close does not move a resolution")
	var resolved int64
	for _, v := range by {
		resolved += v.Resolved
	}
	require.EqualValues(t, 4, resolved, "the reopened ticket and the one resolved before the window count nowhere")
	for _, v := range by {
		require.Zero(t, v.Created, "none was created in the window")
	}

	// Reopening is the clearing of resolved_at: the ticket leaves its old day.
	var id uuid.UUID
	require.NoError(t, f.tx.QueryRowContext(f.ctx, `SELECT id FROM tickets WHERE subject = 'resolved'`).Scan(&id))
	_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET resolved_at = NULL WHERE id = $1`, id)
	require.NoError(t, err)
	require.EqualValues(t, 1, byDay(f.volume(f.all(), "UTC", now))["2026-06-10"].Resolved)
}

func TestVolumeByDay_VisibilityIsTheLists(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gs := groupstore.New(dbgen.New(f.tx))
	g2 := group.Group{ID: uuid.New(), Name: "VolGroup"}
	require.NoError(t, gs.Create(f.ctx, g2))
	require.NoError(t, gs.AddMember(f.ctx, g2.ID, f.scoped.ID))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catB.ID}))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catA.ID, TypeID: &f.typeA.ID}))

	created := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	resolvedAt := ref(time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC))
	mk := func(s spec) {
		s.created = created
		if s.reporter == nil {
			s.reporter = &f.dana // not the requester under test, who is bob
		}
		f.done(f.mk(s), resolvedAt, nil)
	}
	mk(spec{subject: "v reported by me", reporter: &f.scoped, cat: &f.catA, typ: &f.typeB})
	mk(spec{subject: "v assigned to me", cat: &f.catA, typ: &f.typeB, assignee: &f.scoped})
	mk(spec{subject: "v my group", cat: &f.catA, typ: &f.typeB, group: &g2})
	mk(spec{subject: "v covered category", cat: &f.catB})
	mk(spec{subject: "v covered type", cat: &f.catA, typ: &f.typeA})
	mk(spec{subject: "v out of scope", cat: &f.catA, typ: &f.typeB, assignee: &f.staff})
	mk(spec{subject: "v out of scope, no assignee", cat: &f.catA, typ: &f.typeB})
	// The requester's own ticket, which is also out of scope for the scoped staff member.
	mk(spec{subject: "v bob's", reporter: &f.bob, cat: &f.catA, typ: &f.typeB, assignee: &f.staff})

	day := func(fl ticket.Filter) ticket.DayVolume {
		got := f.volume(fl, "UTC", now)
		return byDay(got)["2026-06-14"]
	}
	admin := day(ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll})
	require.EqualValues(t, 8, admin.Created)
	require.EqualValues(t, 8, admin.Resolved)

	scoped := day(ticket.Filter{ActorID: f.scoped.ID, Visibility: ticket.VisibilityScoped})
	require.EqualValues(t, 5, scoped.Created, "reported, assigned, group, covered category, covered type")
	require.EqualValues(t, 5, scoped.Resolved)

	req := day(ticket.Filter{ActorID: f.bob.ID, Visibility: ticket.VisibilityReporter})
	require.EqualValues(t, 1, req.Created)

	// Every arm agrees with the list for the same caller.
	for _, fl := range []ticket.Filter{
		{ActorID: f.scoped.ID, Visibility: ticket.VisibilityScoped},
		{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll},
		{ActorID: f.bob.ID, Visibility: ticket.VisibilityReporter},
	} {
		w := fl
		w.CreatedAfter, w.CreatedBefore = ref(created.Add(-time.Hour)), ref(created.Add(time.Hour))
		n, err := f.ts.CountFilteredAt(f.ctx, w, now)
		require.NoError(t, err)
		require.EqualValues(t, n, day(fl).Created)
	}
}

func TestVolumeByDay_CreatedSumEqualsTheCreatedRangeCount(t *testing.T) {
	f := newSearchFixture(t)
	for _, tz := range []string{"UTC", "Europe/London", "America/New_York", "Pacific/Auckland", "Asia/Kolkata"} {
		t.Run(tz, func(t *testing.T) {
			now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
			loc, err := time.LoadLocation(tz)
			require.NoError(t, err)
			local := now.In(loc)
			first := time.Date(local.Year(), local.Month(), local.Day()-29, 0, 0, 0, 0, loc)
			end := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)

			// Tickets scattered about the edges of the window, in this zone.
			for i, c := range []time.Time{
				first.Add(-time.Second), first, first.Add(time.Second), end.Add(-time.Second), end,
				first.AddDate(0, 0, 14).Add(13 * time.Hour), first.AddDate(0, 0, 27).Add(-time.Minute),
			} {
				f.mk(spec{subject: tz + string(rune('a'+i)), created: c})
			}

			var sum int64
			for _, d := range f.volume(f.all(), tz, now) {
				sum += d.Created
			}
			n, err := f.ts.CountFilteredAt(f.ctx, ticket.Filter{
				ActorID: f.staff.ID, Visibility: ticket.VisibilityAll,
				CreatedAfter: &first, CreatedBefore: &end,
			}, now)
			require.NoError(t, err)
			require.Positive(t, n, "the window holds something")
			require.Equal(t, n, sum)
		})
	}
}

func TestVolumeByDay_UnknownZone(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	_, err := f.ts.VolumeByDay(f.ctx, f.all(), "Mars/Olympus_Mons", now)
	require.ErrorIs(t, err, ticket.ErrUnknownTimeZone)
	// The statement failed, so the transaction is dead: one zone per fixture.
	g := newSearchFixture(t)
	_, err = g.ts.VolumeByDay(g.ctx, g.all(), "Not A Zone", now)
	require.ErrorIs(t, err, ticket.ErrUnknownTimeZone)
	// A zone is a parameter: text that looks like SQL is just an unknown name.
	h := newSearchFixture(t)
	_, err = h.ts.VolumeByDay(h.ctx, h.all(), "UTC'; DROP TABLE tickets; --", now)
	require.ErrorIs(t, err, ticket.ErrUnknownTimeZone)
}

// The resolved arm must be able to use tickets_done_at_idx, and the created arm
// tickets_created_at_idx. A test table is never planned through an index on its
// own, so this takes the alternatives away (in a transaction that rolls back)
// and looks at what the planner picks from what is left: if the arm stopped
// matching the indexed expression, the plan would say Seq Scan. The 1M-row
// shape of the same question is TestListScale (BENCH_LIST).
func TestVolumeByDay_ResolvedArmCanUseItsIndex(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	_, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	for _, setting := range []string{"enable_seqscan", "enable_bitmapscan"} {
		_, err := tx.ExecContext(ctx, `SET LOCAL `+setting+` = off`)
		require.NoError(t, err)
	}
	_, err := tx.ExecContext(ctx, `ANALYZE tickets`)
	require.NoError(t, err)

	rec := &planRecorder{DBTX: tx, tx: tx}
	ts := ticketstore.New(dbgen.New(rec))
	_, err = ts.VolumeByDay(ctx, ticket.Filter{ActorID: uuid.New(), Visibility: ticket.VisibilityAll}, "UTC",
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	plan := rec.plans[len(rec.plans)-1]
	require.Contains(t, plan, "tickets_done_at_idx", "the resolved arm, as an administrator; plan:\n%s", plan)
	require.Contains(t, plan, "tickets_created_at_idx", "the created arm; plan:\n%s", plan)
}
