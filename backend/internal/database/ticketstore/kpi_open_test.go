package ticketstore_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The KPI snapshot queries (KPIOpenByAge, KPIOpenByAssignee, KPIOpenByGroup)
// and Store.KPI, which runs all eight queries of a request (#131, DESIGN.md
// "KPI reports", SQL). Same fixture as kpi_period_test.go.

// kpiNow is the instant these tests read "now" at: Tue 24 Nov 2026 12:00 UTC.
var kpiNow = nov(24, 12, 0)

// ago is the instant d before kpiNow.
func ago(d time.Duration) time.Time { return kpiNow.Add(-d) }

const hour = time.Hour
const dayLen = 24 * time.Hour

// kpiRequest is request() with both periods and now filled, as the handler
// fills it: the 30 days ending 24 Nov, and the 30 before.
func (f *kpiFx) kpiRequest() report.Request {
	req := f.request()
	req.Now = kpiNow
	req.Current = report.Period{From: day(f.t, "2026-10-26"), To: day(f.t, "2026-11-24")}
	req.Previous = req.Current.Previous()
	return req
}

func (f *kpiFx) kpi(req report.Request) report.Raw {
	f.t.Helper()
	raw, err := f.ts.KPI(f.ctx, req)
	require.NoError(f.t, err)
	return raw
}

func (f *kpiFx) setStatus(t ticket.Ticket, name string) {
	f.t.Helper()
	_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET status_id = (SELECT id FROM statuses WHERE name = $2) WHERE id = $1`, t.ID, name)
	require.NoError(f.t, err)
}

func ageRows(rows []report.AgeCount) map[int]int64 {
	m := map[int]int64{}
	for _, r := range rows {
		m[r.Days] += r.Count
	}
	return m
}

func TestKPIOpenNow_IsTheSameSetAsNotResolvedOrClosed(t *testing.T) {
	f := newKPIFx(t)
	mk := func(status string, o tk) {
		f.setStatus(f.mk(o), status)
	}
	mk("New", tk{created: ago(1 * dayLen)})
	mk("In Progress", tk{created: ago(2 * dayLen)})
	mk("Pending", tk{created: ago(3 * dayLen), paused: 60})
	mk("New", tk{created: ago(4 * dayLen), reopens: 1}) // resolved, reopened: open again
	mk("Resolved", tk{created: ago(5 * dayLen), resolved: tp(ago(1 * dayLen))})
	mk("Closed", tk{created: ago(6 * dayLen), resolved: tp(ago(3 * dayLen)), closed: tp(ago(1 * dayLen))})
	mk("Closed", tk{created: ago(7 * dayLen), closed: tp(ago(2 * dayLen))}) // closed without being resolved

	req := f.kpiRequest()
	req.Query.CategoryID = &f.catA.ID // the fixture's own category: nothing else in the database is in it
	got := f.kpi(req)
	var open int64
	for _, a := range got.Ages {
		open += a.Count
	}

	var byStatus int64
	require.NoError(t, f.tx.QueryRowContext(f.ctx, `SELECT count(*) FROM tickets t JOIN statuses s ON s.id = t.status_id
		WHERE t.category_id = $1 AND NOT (s.kind = 'system' AND s.name IN ('Resolved', 'Closed'))`, f.catA.ID).Scan(&byStatus))
	require.EqualValues(t, 4, byStatus, "the fixture has four open tickets")
	require.Equal(t, byStatus, open, "open now (no finish instant) is the tickets not in a Resolved or Closed system status")
}

func TestKPIOpenByAge_Boundaries(t *testing.T) {
	f := newKPIFx(t)
	for _, age := range []time.Duration{
		0,                                    // 0
		2*dayLen + 23*hour,                   // 2: just under three days
		3 * dayLen,                           // 3
		7*dayLen + 23*hour,                   // 7
		8 * dayLen,                           // 8
		30*dayLen + 23*hour + 59*time.Minute, // 30
		31 * dayLen,                          // 31
		90 * dayLen,                          // far over: capped at 31
		-5 * time.Minute,                     // created a moment after now: age 0 in a bucket
	} {
		f.mk(tk{created: ago(age)})
	}
	f.mk(tk{created: ago(1 * dayLen), resolved: tp(ago(1 * hour))}) // finished: not open

	rows, err := ticketstore.KPIOpenByAgeRows(f.ctx, f.q, f.scope(f.kpiRequest()), kpiNow)
	require.NoError(t, err)
	by := ageRows(rows)
	for _, d := range []int{2, 3, 7, 8, 30} {
		require.EqualValues(t, 1, by[d], "age %d", d)
	}
	require.EqualValues(t, 2, by[31], "31 days and 90 days are both 31, the cap")
	require.EqualValues(t, 1, by[0], "an age of 0")
	require.EqualValues(t, 1, by[-1], "a ticket created a moment after now has a negative age, which Buckets reads as 0")
	require.NotContains(t, by, 1, "the finished ticket is not open")
	require.LessOrEqual(t, len(by), 32, "at most 32 rows")

	var counts []int64
	for _, b := range report.Buckets(rows) {
		counts = append(counts, b.Count)
	}
	require.Equal(t, []int64{3, 2, 2, 2}, counts, "0-2 (0, -5m, 2), 3-7 (3, 7), 8-30 (8, 30), over 30 (31, 90)")
}

func TestKPIWorkload(t *testing.T) {
	cases := []struct {
		name       string
		set        func(f *kpiFx) tk
		wantPerson string // "" is the NULL row
		wantGroup  string
	}{
		{"a person and a group count once in each list", func(f *kpiFx) tk { return tk{assignee: &f.staff, group: &f.grp} }, "Sam Staff", "KPI Group One"},
		{"a person only: the group list has the NULL row", func(f *kpiFx) tk { return tk{assignee: &f.staff} }, "Sam Staff", ""},
		{"a group only: the person list has the NULL row", func(f *kpiFx) tk { return tk{group: &f.grp} }, "", "KPI Group One"},
		{"neither: the NULL row in both", func(f *kpiFx) tk { return tk{} }, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKPIFx(t)
			f.mk(tc.set(f))
			req := f.kpiRequest()
			sc := f.scope(req)
			people, err := ticketstore.KPIOpenByAssigneeRows(f.ctx, f.q, sc)
			require.NoError(t, err)
			groups, err := ticketstore.KPIOpenByGroupRows(f.ctx, f.q, sc)
			require.NoError(t, err)
			for _, c := range []struct {
				got  []report.Holder
				want string
			}{{people, tc.wantPerson}, {groups, tc.wantGroup}} {
				require.Len(t, c.got, 1)
				require.EqualValues(t, 1, c.got[0].Count)
				if c.want == "" {
					require.Nil(t, c.got[0].ID)
					require.Nil(t, c.got[0].Name)
				} else {
					require.NotNil(t, c.got[0].ID)
					require.Equal(t, c.want, *c.got[0].Name)
				}
			}
		})
	}
}

func TestKPIWorkload_NamesOfDeletedAndDisabledPeople(t *testing.T) {
	f := newKPIFx(t)
	f.mk(tk{assignee: &f.staff})
	f.mk(tk{assignee: &f.loner})
	f.mk(tk{assignee: &f.loner})
	_, err := f.tx.ExecContext(f.ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, f.staff.ID)
	require.NoError(t, err)
	_, err = f.tx.ExecContext(f.ctx, `UPDATE users SET disabled = true WHERE id = $1`, f.loner.ID)
	require.NoError(t, err)

	people, err := ticketstore.KPIOpenByAssigneeRows(f.ctx, f.q, f.scope(f.kpiRequest()))
	require.NoError(t, err)
	got := map[string]int64{}
	for _, h := range people {
		require.NotNil(t, h.Name, "a deleted or disabled person is still named")
		got[*h.Name] = h.Count
	}
	require.Equal(t, map[string]int64{"Sam Staff": 1, "Lou Loner": 2}, got)
}

func TestKPIWorkload_FinishedTicketsAreNotHeld(t *testing.T) {
	f := newKPIFx(t)
	f.mk(tk{assignee: &f.staff, resolved: tp(ago(1 * dayLen))})
	f.mk(tk{assignee: &f.staff, closed: tp(ago(1 * dayLen))})
	people, err := ticketstore.KPIOpenByAssigneeRows(f.ctx, f.q, f.scope(f.kpiRequest()))
	require.NoError(t, err)
	require.Empty(t, people)
}

// A fixture with a ticket in every state a request reads: created and finished
// in the current period, in the previous, open, and different categories,
// priorities and holders.
func kpiMixFx(t *testing.T) *kpiFx {
	f := newKPIFx(t)
	// current period (26 Oct to 24 Nov)
	f.mk(tk{created: nov(2, 9, 0), resolved: tp(nov(3, 9, 0)), prio: ticket.PriorityHigh})
	f.mk(tk{created: nov(2, 10, 0), resolved: tp(nov(5, 9, 0)), cat: &f.catB, assignee: &f.staff})
	f.mk(tk{created: nov(20, 10, 0), cat: &f.catB, group: &f.grp, prio: ticket.PriorityLow})
	f.mk(tk{created: ago(40 * dayLen), assignee: &f.staff, group: &f.grp, prio: ticket.PriorityCritical}) // open, created before the period
	// previous period (26 Sep to 25 Oct)
	f.mk(tk{created: utc(2026, time.October, 2, 9, 0), resolved: tp(utc(2026, time.October, 4, 9, 0))})
	f.mk(tk{created: utc(2026, time.October, 3, 9, 0), resolved: tp(nov(8, 9, 0)), reopens: 1}) // finished now, created then
	return f
}

func TestKPI_OneSnapshotIsConsistent(t *testing.T) {
	f := kpiMixFx(t)
	req := f.kpiRequest()
	raw := f.kpi(req)

	require.EqualValues(t, 3, raw.Current.Created)
	require.EqualValues(t, 3, raw.Current.Resolved, "the previous period's reopened ticket finished in this one")
	require.EqualValues(t, 3, raw.Previous.Created, "the open ticket made before the period is in the previous one")
	require.EqualValues(t, 1, raw.Previous.Resolved)

	require.Len(t, raw.Days, 30, "one row per day of the current period, zeros included")
	require.Equal(t, "2026-10-26", raw.Days[0].Day)
	require.Equal(t, "2026-11-24", raw.Days[29].Day)
	var created, resolved int64
	for _, d := range raw.Days {
		created, resolved = created+d.Created, resolved+d.Resolved
	}
	require.Equal(t, raw.Current.Created, created, "the daily series sums to the headline")
	require.Equal(t, raw.Current.Resolved, resolved)

	var cats, prios int64
	for _, c := range raw.ByCategory {
		cats += c.Count
	}
	for _, p := range raw.ByPriority {
		prios += p.Count
	}
	require.Equal(t, raw.Current.Created, cats)
	require.Equal(t, raw.Current.Created, prios)

	var backlog, people, groups int64
	for _, a := range raw.Ages {
		backlog += a.Count
	}
	for _, h := range raw.ByAssignee {
		people += h.Count
	}
	for _, h := range raw.ByGroup {
		groups += h.Count
	}
	require.EqualValues(t, 2, backlog)
	require.Equal(t, backlog, people, "the workload total equals the backlog total")
	require.Equal(t, backlog, groups)
}

func TestKPI_FiltersNarrowTheSnapshotsToo(t *testing.T) {
	f := kpiMixFx(t)
	req := f.kpiRequest()
	req.Query.CategoryID = &f.catB.ID
	raw := f.kpi(req)
	require.EqualValues(t, 2, raw.Current.Created)
	var backlog int64
	for _, a := range raw.Ages {
		backlog += a.Count
	}
	require.EqualValues(t, 1, backlog, "only the open ticket in category B")
	require.Len(t, raw.ByGroup, 1)
	require.Equal(t, f.grp.ID, *raw.ByGroup[0].ID)

	req = f.kpiRequest()
	req.Query.Priorities = []ticket.Priority{ticket.PriorityCritical}
	raw = f.kpi(req)
	require.Zero(t, raw.Current.Created)
	require.Len(t, raw.Ages, 1)

	req = f.kpiRequest()
	req.Query.GroupID = ptr(uuid.New())
	raw = f.kpi(req)
	require.Equal(t, report.PeriodRow{}, raw.Current, "an id that names nothing is zeros, not an error")
	require.Empty(t, raw.Ages)
	require.Empty(t, raw.ByAssignee)
	require.Empty(t, raw.ByGroup)
	require.Empty(t, raw.ByCategory)
}

// scopedFx is visFx's six tickets as open and finished ones in every
// breakdown: a scoped caller must see nothing outside their scope in any of
// the eight queries.
func scopedFx(t *testing.T) *kpiFx {
	f := newKPIFx(t)
	require.NoError(t, groupstore.New(f.q).AddScope(f.ctx, group.GroupScope{GroupID: f.grp.ID, CategoryID: f.catA.ID}))
	recent := nov(10, 9, 0)
	mk := func(o tk) {
		o.created = recent
		f.mk(o)
		o.resolved = tp(nov(11, 9, 0))
		f.mk(o)
	}
	mk(tk{cat: &f.catA})                     // the group's category
	mk(tk{cat: &f.catB, assignee: &f.staff}) // assigned to Sam
	mk(tk{cat: &f.catB, group: &f.grp})      // assigned to Sam's group
	mk(tk{cat: &f.catB, reporter: &f.staff}) // reported by Sam
	mk(tk{cat: &f.catB, assignee: &f.loner}) // nothing links it to Sam
	mk(tk{cat: &f.catB, reporter: &f.loner, group: &f.grp2})
	return f
}

func TestKPI_Visibility(t *testing.T) {
	cases := []struct {
		name  string
		actor func(f *kpiFx) uuid.UUID
		vis   ticket.Visibility
		want  int64 // tickets visible, each of the six shapes twice (open, finished)
	}{
		{"an administrator sees every ticket", func(f *kpiFx) uuid.UUID { return f.admin.ID }, ticket.VisibilityAll, 6},
		{"staff with enforcement off see every ticket", func(f *kpiFx) uuid.UUID { return f.staff.ID }, ticket.VisibilityAll, 6},
		{"scoped staff see their scope", func(f *kpiFx) uuid.UUID { return f.staff.ID }, ticket.VisibilityScoped, 4},
		{"scoped staff in no group see what they reported or hold", func(f *kpiFx) uuid.UUID { return f.loner.ID }, ticket.VisibilityScoped, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := scopedFx(t)
			req := f.kpiRequest()
			req.ActorID, req.Visibility = tc.actor(f), tc.vis
			raw := f.kpi(req)

			require.Equal(t, 2*tc.want, raw.Current.Created)
			var cats, prios, series int64
			for _, c := range raw.ByCategory {
				cats += c.Count
			}
			for _, p := range raw.ByPriority {
				prios += p.Count
			}
			for _, d := range raw.Days {
				series += d.Created
			}
			require.Equal(t, 2*tc.want, cats)
			require.Equal(t, 2*tc.want, prios)
			require.Equal(t, 2*tc.want, series)

			var backlog, people, groups int64
			for _, a := range raw.Ages {
				backlog += a.Count
			}
			for _, h := range raw.ByAssignee {
				people += h.Count
			}
			for _, h := range raw.ByGroup {
				groups += h.Count
			}
			require.Equal(t, tc.want, backlog, "the backlog is scoped")
			require.Equal(t, tc.want, people, "the person list is scoped")
			require.Equal(t, tc.want, groups, "the group list is scoped")
			if tc.vis == ticket.VisibilityScoped && tc.actor(f) == f.staff.ID {
				for _, h := range raw.ByAssignee {
					if h.ID != nil {
						require.Equal(t, f.staff.ID, *h.ID, "Lou's ticket is outside Sam's scope, so Lou is not listed")
					}
				}
				for _, h := range raw.ByGroup {
					if h.ID != nil {
						require.Equal(t, f.grp.ID, *h.ID, "the other group is outside Sam's scope")
					}
				}
			}
		})
	}
}

func TestKPI_UnknownZone(t *testing.T) {
	f := newKPIFx(t)
	req := f.kpiRequest()
	req.Query.TZ = "Mars/Olympus_Mons"
	_, err := f.ts.KPI(f.ctx, req)
	require.Error(t, err, "a zone Postgres does not know fails the request, as the handler's earlier Today would have")
}
