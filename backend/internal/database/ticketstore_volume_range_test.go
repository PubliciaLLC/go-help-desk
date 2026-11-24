package database_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// KPI reports (#131) generalise #675's trend to any date range and three
// filters, and add LocalToday. The 30-day trend's own tests stay in
// ticketstore_volume_test.go and still run against the same query.

func TestToday(t *testing.T) {
	cases := []struct {
		name string
		tz   string
		now  time.Time
		want string
	}{
		{"UTC, just before midnight", "UTC", time.Date(2026, 6, 15, 23, 59, 59, 0, time.UTC), "2026-06-15"},
		{"UTC, midnight", "UTC", time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC), "2026-06-16"},
		{"London in summer time, 23:30 local", "Europe/London", at(t, "Europe/London", 2026, 6, 15, 23, 30), "2026-06-15"},
		{"London in summer time, 00:30 local is the day before in UTC", "Europe/London", at(t, "Europe/London", 2026, 6, 16, 0, 30), "2026-06-16"},
		{"London in summer time, one second before local midnight", "Europe/London", at(t, "Europe/London", 2026, 6, 15, 23, 59).Add(59 * time.Second), "2026-06-15"},
		{"London in winter time, local midnight is UTC midnight", "Europe/London", time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC), "2026-01-16"},
		{"New York, 23:30 local is the next day in UTC", "America/New_York", at(t, "America/New_York", 2026, 6, 15, 23, 30), "2026-06-15"},
		{"New York, 00:30 local", "America/New_York", at(t, "America/New_York", 2026, 6, 16, 0, 30), "2026-06-16"},
		{"New York, one second before local midnight", "America/New_York", at(t, "America/New_York", 2026, 6, 15, 23, 59).Add(59 * time.Second), "2026-06-15"},
		{"New York, the 25-hour day, 23:30 local", "America/New_York", at(t, "America/New_York", 2026, 11, 1, 23, 30), "2026-11-01"},
		{"New York, the first instant of the day after", "America/New_York", time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC), "2026-11-02"},
		{"New York, the last instant of the 25-hour day", "America/New_York", time.Date(2026, 11, 2, 4, 59, 59, 0, time.UTC), "2026-11-01"},
		{"a zone ahead of UTC, early evening UTC is tomorrow there", "Pacific/Auckland", time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC), "2026-06-16"},
	}
	f := newSearchFixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.ts.Today(f.ctx, tc.tz, tc.now)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.String())
			require.Equal(t, time.UTC, got.Time().Location(), "a Day is at 00:00 UTC")
			require.Zero(t, got.Time().Hour()+got.Time().Minute()+got.Time().Second())
		})
	}
}

func TestToday_UnknownZone(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// The failed statement kills the transaction, so one zone per fixture.
	for _, tz := range []string{"Mars/Olympus_Mons", "Not A Zone", "UTC'; DROP TABLE tickets; --"} {
		t.Run(tz, func(t *testing.T) {
			f := newSearchFixture(t)
			_, err := f.ts.Today(f.ctx, tz, now)
			require.ErrorIs(t, err, ticket.ErrUnknownTimeZone)
		})
	}
}

// rangeVolume runs the generalised query for an administrator.
func (f *searchFixture) rangeVolume(from, to, tz string, fl func(*dbgen.TicketVolumeByDayParams)) []dbgen.TicketVolumeByDayRow {
	f.t.Helper()
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		require.NoError(f.t, err)
		return d
	}
	p := dbgen.TicketVolumeByDayParams{
		Tz: tz, FromDay: day(from), ToDay: day(to),
		Unrestricted: true, ActorID: f.staff.ID,
		Priorities: []string{},
	}
	if fl != nil {
		fl(&p)
	}
	rows, err := dbgen.New(f.tx).TicketVolumeByDay(f.ctx, p)
	require.NoError(f.t, err)
	return rows
}

func volumeDays(rows []dbgen.TicketVolumeByDayRow) (days []string, created, resolved int64) {
	for _, r := range rows {
		days = append(days, r.Day.Format("2006-01-02"))
		created += r.Created
		resolved += r.Resolved
	}
	return
}

func TestVolumeRange_OneRowPerDayOldestFirst(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		tz       string
		rows     int
		first    string
		last     string
		empty    bool // no ticket of the fixture falls in the window
	}{
		{"a single day", "2026-06-15", "2026-06-15", "UTC", 1, "2026-06-15", "2026-06-15", true},
		{"a custom 7-day range", "2026-06-09", "2026-06-15", "UTC", 7, "2026-06-09", "2026-06-15", true},
		{"7 days across the New York change to winter time", "2026-10-29", "2026-11-04", "America/New_York", 7, "2026-10-29", "2026-11-04", true},
		{"366 days across a leap day", "2023-12-31", "2024-12-30", "UTC", 366, "2023-12-31", "2024-12-30", true},
		{"366 days in London, across both changes", "2026-01-01", "2027-01-01", "Europe/London", 366, "2026-01-01", "2027-01-01", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSearchFixture(t)
			days, c, r := volumeDays(f.rangeVolume(tc.from, tc.to, tc.tz, nil))
			require.Len(t, days, tc.rows)
			require.Equal(t, tc.first, days[0], "oldest first")
			require.Equal(t, tc.last, days[len(days)-1])
			for i := 1; i < len(days); i++ {
				a, _ := time.Parse("2006-01-02", days[i-1])
				b, _ := time.Parse("2006-01-02", days[i])
				require.Equal(t, a.AddDate(0, 0, 1), b, "consecutive days, none missing or repeated")
			}
			if tc.empty {
				require.Zero(t, c+r, "every row is zeros")
				for _, row := range f.rangeVolume(tc.from, tc.to, tc.tz, nil) {
					require.Zero(t, row.Created+row.Resolved)
				}
			} else {
				require.EqualValues(t, 6, c, "the fixture's six January tickets, and zeros on the other days")
			}
		})
	}
}

func TestVolumeRange_EdgesAreLocalMidnightsAndTheEndDayIsIncluded(t *testing.T) {
	// A 7-day window, 9 to 15 June, counted in New York (UTC-4 in June).
	f := newSearchFixture(t)
	first := at(t, "America/New_York", 2026, 6, 9, 0, 0)
	end := at(t, "America/New_York", 2026, 6, 16, 0, 0) // first instant after the 15th
	resolved := func(tm time.Time) *time.Time { return &tm }
	for i, c := range []time.Time{
		first.Add(-time.Microsecond), first, first.Add(time.Second),
		end.Add(-time.Second), end, end.Add(time.Hour),
	} {
		tk := f.mk(spec{subject: "edge" + string(rune('a'+i)), created: c})
		f.done(tk, resolved(c), nil)
	}
	days, created, res := volumeDays(f.rangeVolume("2026-06-09", "2026-06-15", "America/New_York", nil))
	require.Len(t, days, 7)
	require.EqualValues(t, 3, created, "the local midnight that opens the range, a second after it and the last second of the end day are in; the microsecond before and everything from the next midnight are out")
	require.EqualValues(t, 3, res, "the resolved arm has the same bounds as the created arm")

	by := map[string]dbgen.TicketVolumeByDayRow{}
	for _, r := range f.rangeVolume("2026-06-09", "2026-06-15", "America/New_York", nil) {
		by[r.Day.Format("2006-01-02")] = r
	}
	require.EqualValues(t, 2, by["2026-06-09"].Created)
	require.EqualValues(t, 1, by["2026-06-15"].Created, "the end day is included, to its last second")
	require.EqualValues(t, 1, by["2026-06-15"].Resolved)
}

func TestVolumeRange_ASingleRangeSumsToTheCreatedCount(t *testing.T) {
	f := newSearchFixture(t)
	for i := 0; i < 5; i++ {
		tk := f.mk(spec{subject: "sum" + string(rune('a'+i)), created: time.Date(2026, 6, 10+i, 12, 0, 0, 0, time.UTC)})
		if i%2 == 0 {
			f.done(tk, ref(time.Date(2026, 6, 11+i, 12, 0, 0, 0, time.UTC)), nil)
		}
	}
	days, created, resolved := volumeDays(f.rangeVolume("2026-06-09", "2026-06-17", "UTC", nil))
	require.Len(t, days, 9)
	require.EqualValues(t, 5, created)
	require.EqualValues(t, 3, resolved)
}

func TestVolumeRange_EachFilterNarrowsBothArms(t *testing.T) {
	f := newSearchFixture(t)
	gs := groupstore.New(dbgen.New(f.tx))
	g2 := group.Group{ID: uuid.New(), Name: "VolFilterGroup"}
	require.NoError(t, gs.Create(f.ctx, g2))

	d := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	mk := func(s spec) {
		s.created = d
		f.done(f.mk(s), ref(d.Add(time.Hour)), nil)
	}
	mk(spec{subject: "f a high g1", cat: &f.catA, typ: &f.typeA, prio: ticket.PriorityHigh, group: &f.group})
	mk(spec{subject: "f a low g2", cat: &f.catA, prio: ticket.PriorityLow, group: &g2})
	mk(spec{subject: "f b high none", cat: &f.catB, prio: ticket.PriorityHigh})
	mk(spec{subject: "f b critical g1", cat: &f.catB, prio: ticket.PriorityCritical, group: &f.group})
	mk(spec{subject: "f a medium none", cat: &f.catA, prio: ticket.PriorityMedium})

	nul := func(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: true} }
	cases := []struct {
		name string
		set  func(*dbgen.TicketVolumeByDayParams)
		want int64
	}{
		{"no filter", nil, 5},
		{"empty priorities is no filter", func(p *dbgen.TicketVolumeByDayParams) { p.Priorities = []string{} }, 5},
		{"category A, with its types and items", func(p *dbgen.TicketVolumeByDayParams) { p.CategoryID = nul(f.catA.ID) }, 3},
		{"category B", func(p *dbgen.TicketVolumeByDayParams) { p.CategoryID = nul(f.catB.ID) }, 2},
		{"a category that does not exist matches nothing", func(p *dbgen.TicketVolumeByDayParams) { p.CategoryID = nul(uuid.New()) }, 0},
		{"one priority", func(p *dbgen.TicketVolumeByDayParams) { p.Priorities = []string{"high"} }, 2},
		{"two priorities", func(p *dbgen.TicketVolumeByDayParams) { p.Priorities = []string{"high", "critical"} }, 3},
		{"group 1", func(p *dbgen.TicketVolumeByDayParams) { p.AssigneeGroupID = nul(f.group.ID) }, 2},
		{"group 2", func(p *dbgen.TicketVolumeByDayParams) { p.AssigneeGroupID = nul(g2.ID) }, 1},
		{"a group that does not exist matches nothing", func(p *dbgen.TicketVolumeByDayParams) { p.AssigneeGroupID = nul(uuid.New()) }, 0},
		{"all three are ANDed", func(p *dbgen.TicketVolumeByDayParams) {
			p.CategoryID, p.Priorities, p.AssigneeGroupID = nul(f.catB.ID), []string{"critical"}, nul(f.group.ID)
		}, 1},
		{"all three, no overlap", func(p *dbgen.TicketVolumeByDayParams) {
			p.CategoryID, p.Priorities, p.AssigneeGroupID = nul(f.catA.ID), []string{"critical"}, nul(f.group.ID)
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := f.rangeVolume("2026-06-12", "2026-06-12", "UTC", tc.set)
			require.Len(t, rows, 1)
			require.Equal(t, tc.want, rows[0].Created, "the created arm")
			require.Equal(t, tc.want, rows[0].Resolved, "the resolved arm")
		})
	}
}

func TestVolumeRange_FiltersSeeTheCurrentValues(t *testing.T) {
	// A ticket moved from one category to another counts in the new one, in
	// both arms: the filter is on the row, not on any history.
	f := newSearchFixture(t)
	d := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	tk := f.mk(spec{subject: "moved", cat: &f.catA, created: d})
	f.done(tk, ref(d.Add(time.Hour)), nil)
	_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET category_id = $2 WHERE id = $1`, tk.ID, f.catB.ID)
	require.NoError(t, err)

	inA := f.rangeVolume("2026-06-12", "2026-06-12", "UTC", func(p *dbgen.TicketVolumeByDayParams) {
		p.CategoryID = uuid.NullUUID{UUID: f.catA.ID, Valid: true}
	})
	inB := f.rangeVolume("2026-06-12", "2026-06-12", "UTC", func(p *dbgen.TicketVolumeByDayParams) {
		p.CategoryID = uuid.NullUUID{UUID: f.catB.ID, Valid: true}
	})
	require.Zero(t, inA[0].Created+inA[0].Resolved)
	require.EqualValues(t, 1, inB[0].Created)
	require.EqualValues(t, 1, inB[0].Resolved)
}

func TestVolumeRange_UnknownZone(t *testing.T) {
	f := newSearchFixture(t)
	_, err := dbgen.New(f.tx).TicketVolumeByDay(f.ctx, dbgen.TicketVolumeByDayParams{
		Tz: "Mars/Olympus_Mons", FromDay: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		ToDay: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC), Unrestricted: true, ActorID: f.staff.ID,
		Priorities: []string{},
	})
	require.Error(t, err, "the query itself still fails on an unknown zone; localToday is what maps it")
}
