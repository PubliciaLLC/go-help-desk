package ticketstore_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// KPIPeriod, KPICreatedByCategory and KPICreatedByPriority (#131, DESIGN.md
// "KPI reports", SQL) run against Postgres: the work is AT TIME ZONE,
// percentile_cont and a lateral over sla_records, which a fake would restate.
// Each test runs in a rolled-back transaction.

type kpiFx struct {
	t   *testing.T
	ctx context.Context
	tx  *sql.Tx
	q   *dbgen.Queries
	ts  *ticketstore.Store

	admin, staff, loner, rep user.User
	catA, catB               category.Category
	grp, grp2                group.Group
	stNew                    ticket.Status
	policy                   uuid.UUID
	seq                      int64
}

func newKPIFx(t *testing.T) *kpiFx {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)
	ctx := context.Background()
	f := &kpiFx{t: t, ctx: ctx, tx: tx, q: q, ts: ticketstore.New(q), seq: 5000}

	us := userstore.New(q)
	mkUser := func(name string, role user.Role) user.User {
		u := user.User{ID: uuid.New(), Email: name + "-" + uuid.NewString()[:8] + "@example.com", DisplayName: name, Role: role,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	f.admin = mkUser("Ada Admin", user.RoleAdmin)
	f.staff = mkUser("Sam Staff", user.RoleStaff)
	f.loner = mkUser("Lou Loner", user.RoleStaff)
	f.rep = mkUser("Rae Reporter", user.RoleUser)

	f.catA = f.mkCategory("Alpha")
	f.catB = f.mkCategory("Bravo")

	gs := groupstore.New(q)
	f.grp = group.Group{ID: uuid.New(), Name: "KPI Group One"}
	f.grp2 = group.Group{ID: uuid.New(), Name: "KPI Group Two"}
	require.NoError(t, gs.Create(ctx, f.grp))
	require.NoError(t, gs.Create(ctx, f.grp2))
	require.NoError(t, gs.AddMember(ctx, f.grp.ID, f.staff.ID))

	st, err := f.ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	f.stNew = st

	f.policy = uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO sla_policies (id, name, response_target_min, resolution_target_min) VALUES ($1, $2, 60, 480)`,
		f.policy, "KPI policy "+f.policy.String())
	require.NoError(t, err)
	return f
}

func (f *kpiFx) mkCategory(name string) category.Category {
	f.t.Helper()
	c := category.Category{ID: uuid.New(), Name: name + " " + uuid.NewString()[:6], SortOrder: 1, Active: true}
	require.NoError(f.t, categorystore.New(f.q).CreateCategory(f.ctx, c))
	return c
}

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// nov is a November 2026 instant in UTC, the month most of these tests report.
func nov(d, h, min int) time.Time { return utc(2026, time.November, d, h, min) }

func tp(t time.Time) *time.Time { return &t }
func i64(n int64) *int64        { return &n }

// tk is one ticket. Zero fields take the fixture's defaults: created Nov 10
// 09:00 UTC, medium, the first category, reported by rep, not finished.
type tk struct {
	created  time.Time
	resolved *time.Time
	closed   *time.Time
	paused   int64
	reopens  int
	prio     ticket.Priority
	cat      *category.Category
	group    *group.Group
	assignee *user.User
	reporter *user.User
}

func (f *kpiFx) mk(o tk) ticket.Ticket {
	f.t.Helper()
	if o.created.IsZero() {
		o.created = nov(10, 9, 0)
	}
	if o.prio == "" {
		o.prio = ticket.PriorityMedium
	}
	if o.cat == nil {
		o.cat = &f.catA
	}
	if o.reporter == nil {
		o.reporter = &f.rep
	}
	f.seq++
	t := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, f.seq),
		Subject: "kpi", Description: "kpi", DescriptionFormat: ticket.TextFormatPlain,
		SourceChannel: ticket.ChannelWeb, CategoryID: o.cat.ID, Priority: o.prio, StatusID: f.stNew.ID,
		ReporterUserID: &o.reporter.ID, CreatedAt: o.created, UpdatedAt: o.created,
	}
	if o.assignee != nil {
		t.AssigneeUserID = &o.assignee.ID
	}
	if o.group != nil {
		t.AssigneeGroupID = &o.group.ID
	}
	require.NoError(f.t, f.ts.Create(f.ctx, t))
	_, err := f.tx.ExecContext(f.ctx,
		`UPDATE tickets SET resolved_at = $2, closed_at = $3, sla_paused_seconds = $4, reopen_count = $5 WHERE id = $1`,
		t.ID, o.resolved, o.closed, o.paused, o.reopens)
	require.NoError(f.t, err)
	return t
}

// rec is one sla_records row of a ticket.
type rec struct {
	superseded bool
	firstResp  *time.Time
	frozen     *int64 // response_elapsed_at_met_seconds
	started    *time.Time
	respBreach bool
	resBreach  bool
}

func (f *kpiFx) rec(t ticket.Ticket, r rec) {
	f.t.Helper()
	var sup, rb, nb *time.Time
	if r.superseded {
		sup = tp(nov(20, 0, 0))
	}
	if r.respBreach {
		rb = tp(nov(11, 0, 0))
	}
	if r.resBreach {
		nb = tp(nov(12, 0, 0))
	}
	_, err := f.tx.ExecContext(f.ctx, `INSERT INTO sla_records
		(ticket_id, policy_id, first_response_at, response_elapsed_at_met_seconds, started_at,
		 response_breached_at, resolution_breached_at, superseded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		t.ID, f.policy, r.firstResp, r.frozen, r.started, rb, nb, sup)
	require.NoError(f.t, err)
}

// request is a KPI request from the administrator, in UTC, with no filters.
func (f *kpiFx) request() report.Request {
	return report.Request{
		ActorID: f.admin.ID, Visibility: ticket.VisibilityAll,
		Query: report.Query{TZ: "UTC"},
	}
}

func day(t *testing.T, s string) report.Day {
	t.Helper()
	d, err := report.ParseDay(s)
	require.NoError(t, err)
	return d
}

func (f *kpiFx) novPeriod() report.Period {
	return report.Period{From: day(f.t, "2026-11-01"), To: day(f.t, "2026-11-30")}
}

func (f *kpiFx) scope(req report.Request) ticketstore.KPIScope {
	f.t.Helper()
	sc, err := f.ts.KPIScopeFor(f.ctx, req)
	require.NoError(f.t, err)
	return sc
}

func (f *kpiFx) period(req report.Request, p report.Period) report.PeriodRow {
	f.t.Helper()
	row, err := ticketstore.KPIPeriodRow(f.ctx, f.q, f.scope(req), p)
	require.NoError(f.t, err)
	return row
}

// novRow is KPIPeriod for November 2026 as the administrator.
func (f *kpiFx) novRow() report.PeriodRow { return f.period(f.request(), f.novPeriod()) }

func TestKPIPeriod_TimeToResolve(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *kpiFx)
		want  report.PeriodRow
	}{
		{"nothing at all: counts are zero and the medians are NULL", func(f *kpiFx) {}, report.PeriodRow{}},
		{"no pause: finish less creation", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(10, 11, 0))})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(7200), ResolveP90Seconds: i64(7200)}},
		{"two Pending intervals are the paused total, and are taken out", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(10, 12, 0)), paused: 3600 + 1800})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(10800 - 5400), ResolveP90Seconds: i64(10800 - 5400)}},
		{"resolved, reopened, resolved: from creation, the time in Resolved is included", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(12, 9, 0)), reopens: 1})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(172800), ResolveP90Seconds: i64(172800), Reopened: 1}},
		{"closed without being resolved finishes when it was closed", func(f *kpiFx) {
			f.mk(tk{closed: tp(nov(11, 9, 0))})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(86400), ResolveP90Seconds: i64(86400)}},
		{"resolved and then closed finishes when it was resolved", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(10, 10, 0)), closed: tp(nov(20, 10, 0))})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(3600), ResolveP90Seconds: i64(3600)}},
		{"reopened and open is not finished: created counts, nothing else", func(f *kpiFx) {
			f.mk(tk{reopens: 1})
		}, report.PeriodRow{Created: 1}},
		{"a pause longer than the elapsed time is floored at 0", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(10, 10, 0)), paused: 100000})
		}, report.PeriodRow{Created: 1, Resolved: 1, ResolveMedianSeconds: i64(0), ResolveP90Seconds: i64(0)}},
		{"an odd count: the middle value, and 0.9 of the way between the top two", func(f *kpiFx) {
			for h := 1; h <= 5; h++ {
				f.mk(tk{resolved: tp(nov(10, 9+h, 0))})
			}
		}, report.PeriodRow{Created: 5, Resolved: 5, ResolveMedianSeconds: i64(3 * 3600), ResolveP90Seconds: i64(16560)}},
		{"an even count: the mean of the middle two", func(f *kpiFx) {
			for h := 1; h <= 4; h++ {
				f.mk(tk{resolved: tp(nov(10, 9+h, 0))})
			}
		}, report.PeriodRow{Created: 4, Resolved: 4, ResolveMedianSeconds: i64(9000), ResolveP90Seconds: i64(13320)}},
		{"finished in the period but created before it: resolved counts, created does not", func(f *kpiFx) {
			f.mk(tk{created: utc(2026, time.October, 31, 9, 0), resolved: tp(nov(1, 9, 0))})
		}, report.PeriodRow{Resolved: 1, ResolveMedianSeconds: i64(86400), ResolveP90Seconds: i64(86400)}},
		{"created in the period and finished after it: created counts, resolved does not", func(f *kpiFx) {
			f.mk(tk{created: nov(30, 9, 0), resolved: tp(utc(2026, time.December, 1, 9, 0))})
		}, report.PeriodRow{Created: 1}},
		{"the percentile is rounded to whole seconds", func(f *kpiFx) {
			f.mk(tk{resolved: tp(nov(10, 9, 0).Add(1 * time.Second))})
			f.mk(tk{resolved: tp(nov(10, 9, 0).Add(2 * time.Second))})
			f.mk(tk{resolved: tp(nov(10, 9, 0).Add(2 * time.Second))})
		}, report.PeriodRow{Created: 3, Resolved: 3, ResolveMedianSeconds: i64(2), ResolveP90Seconds: i64(2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKPIFx(t)
			tc.setup(f)
			require.Equal(t, tc.want, f.novRow())
		})
	}
}

func TestKPIPeriod_FirstResponse(t *testing.T) {
	done := func(f *kpiFx) ticket.Ticket { return f.mk(tk{resolved: tp(nov(10, 12, 0))}) }
	fr := tp(nov(10, 10, 0))
	cases := []struct {
		name       string
		setup      func(f *kpiFx)
		wantCount  int64
		wantMedian *int64
	}{
		{"no records: no count and a NULL median", func(f *kpiFx) { done(f) }, 0, nil},
		{"the frozen reading of one ticket", func(f *kpiFx) {
			f.rec(done(f), rec{firstResp: fr, frozen: i64(600)})
		}, 1, i64(600)},
		{"the median over three tickets", func(f *kpiFx) {
			f.rec(done(f), rec{firstResp: fr, frozen: i64(600)})
			f.rec(done(f), rec{firstResp: fr, frozen: i64(1800)})
			f.rec(done(f), rec{firstResp: fr, frozen: i64(1200)})
		}, 3, i64(1200)},
		{"the median over two tickets is their mean", func(f *kpiFx) {
			f.rec(done(f), rec{firstResp: fr, frozen: i64(600)})
			f.rec(done(f), rec{firstResp: fr, frozen: i64(1000)})
		}, 2, i64(800)},
		{"a ticket with two original-clock records takes the earlier first response", func(f *kpiFx) {
			t := done(f)
			f.rec(t, rec{superseded: true, firstResp: tp(nov(10, 11, 0)), frozen: i64(900)})
			f.rec(t, rec{firstResp: fr, frozen: i64(600)})
		}, 1, i64(600)},
		{"a record a reopen started is not read", func(f *kpiFx) {
			f.rec(done(f), rec{firstResp: fr, frozen: i64(50), started: tp(nov(11, 0, 0))})
		}, 0, nil},
		{"a reopen-started record does not displace the original", func(f *kpiFx) {
			t := done(f)
			f.rec(t, rec{superseded: true, firstResp: fr, frozen: i64(600)})
			f.rec(t, rec{firstResp: tp(nov(9, 0, 0)), frozen: i64(5), started: tp(nov(11, 0, 0))})
		}, 1, i64(600)},
		{"a record with no frozen reading is not read", func(f *kpiFx) {
			f.rec(done(f), rec{firstResp: fr})
		}, 0, nil},
		{"a record with no first response is not read", func(f *kpiFx) {
			f.rec(done(f), rec{frozen: i64(600)})
		}, 0, nil},
		{"an unfinished ticket's record is not read", func(f *kpiFx) {
			f.rec(f.mk(tk{}), rec{firstResp: fr, frozen: i64(600)})
		}, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKPIFx(t)
			tc.setup(f)
			got := f.novRow()
			require.Equal(t, tc.wantCount, got.FirstResponseCount)
			require.Equal(t, tc.wantMedian, got.FirstResponseMedianSeconds)
		})
	}
}

func TestKPIPeriod_SLAAttainment(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *kpiFx, t ticket.Ticket)
		want  [4]int64 // tracked, met, response met, resolution met
	}{
		{"no record: not tracked", func(f *kpiFx, t ticket.Ticket) {}, [4]int64{0, 0, 0, 0}},
		{"a clean current record: met on both", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{})
		}, [4]int64{1, 1, 1, 1}},
		{"a response breach only", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{respBreach: true})
		}, [4]int64{1, 0, 0, 1}},
		{"a resolution breach only", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{resBreach: true})
		}, [4]int64{1, 0, 1, 0}},
		{"both breached", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{respBreach: true, resBreach: true})
		}, [4]int64{1, 0, 0, 0}},
		{"a breach on a superseded record with a clean current one is breached", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{superseded: true, respBreach: true})
			f.rec(t, rec{})
		}, [4]int64{1, 0, 0, 1}},
		{"only superseded records: never judged, neither tracked nor breached", func(f *kpiFx, t ticket.Ticket) {
			f.rec(t, rec{superseded: true, respBreach: true, resBreach: true})
		}, [4]int64{0, 0, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKPIFx(t)
			tc.setup(f, f.mk(tk{resolved: tp(nov(10, 12, 0))}))
			got := f.novRow()
			require.Equal(t, tc.want, [4]int64{got.SLATracked, got.SLAMet, got.SLAResponseMet, got.SLAResolutionMet})
			require.EqualValues(t, 1, got.Resolved, "the ticket is finished whether or not it was tracked")
		})
	}

	t.Run("one ticket of each kind in one period", func(t *testing.T) {
		f := newKPIFx(t)
		fin := func() ticket.Ticket { return f.mk(tk{resolved: tp(nov(10, 12, 0))}) }
		f.rec(fin(), rec{})
		f.rec(fin(), rec{respBreach: true})
		f.rec(fin(), rec{resBreach: true})
		fin()
		got := f.novRow()
		require.EqualValues(t, [4]int64{3, 1, 2, 2}, [4]int64{got.SLATracked, got.SLAMet, got.SLAResponseMet, got.SLAResolutionMet})
	})

	t.Run("an unfinished ticket with a breach is not counted", func(t *testing.T) {
		f := newKPIFx(t)
		f.rec(f.mk(tk{}), rec{respBreach: true})
		require.EqualValues(t, 0, f.novRow().SLATracked)
	})
}

func TestKPIPeriod_Reopened(t *testing.T) {
	cases := []struct {
		name    string
		reopens []int
		want    int64
	}{
		{"never reopened", []int{0}, 0},
		{"reopened twice is one ticket", []int{2}, 1},
		{"a mix counts tickets, not reopens", []int{0, 2, 1, 0}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKPIFx(t)
			for _, n := range tc.reopens {
				f.mk(tk{resolved: tp(nov(10, 12, 0)), reopens: n})
			}
			got := f.novRow()
			require.Equal(t, tc.want, got.Reopened)
			require.EqualValues(t, len(tc.reopens), got.Resolved)
		})
	}
}

func TestKPIPeriod_LocalDayBoundaries(t *testing.T) {
	// 1 November 2026 is the day New York goes back to winter time (02:00 EDT
	// becomes 01:00 EST), so its local day is 25 hours long: 04:00Z to 05:00Z
	// the next day. Every ticket here is created and resolved at one instant,
	// so created and resolved must agree on which day it is.
	instants := []struct {
		name string
		at   time.Time
	}{
		{"03:59 UTC, in Tokyo's day, not New York's", utc(2026, time.November, 1, 3, 59)},
		{"New York's first instant of the day", utc(2026, time.November, 1, 4, 0)},
		{"23:30 UTC, 18:30 in New York, 08:30 the next day in Tokyo", utc(2026, time.November, 1, 23, 30)},
		{"UTC's until_at itself", utc(2026, time.November, 2, 0, 0)},
		{"22:00 in New York", utc(2026, time.November, 2, 3, 0)},
		{"23:30 in New York", utc(2026, time.November, 2, 4, 30)},
		{"New York's until_at itself", utc(2026, time.November, 2, 5, 0)},
	}
	cases := []struct {
		tz   string
		want int64
	}{
		{"UTC", 3},              // 03:59, 04:00, 23:30
		{"Europe/London", 3},    // GMT again since 25 October, so UTC's day
		{"America/New_York", 5}, // 04:00 .. 04:30 the next day, until_at excluded
		{"Asia/Tokyo", 2},       // 31 Oct 15:00Z up to 1 Nov 15:00Z: 03:59 and 04:00
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			f := newKPIFx(t)
			for _, in := range instants {
				f.mk(tk{created: in.at, resolved: tp(in.at)})
			}
			req := f.request()
			req.Query.TZ = tc.tz
			got := f.period(req, report.Period{From: day(t, "2026-11-01"), To: day(t, "2026-11-01")})
			require.Equal(t, tc.want, got.Created, "created")
			require.Equal(t, tc.want, got.Resolved, "resolved")
		})
	}
}

func TestKPIPeriod_UnknownZone(t *testing.T) {
	f := newKPIFx(t)
	req := f.request()
	req.Query.TZ = "Mars/Olympus_Mons"
	_, err := ticketstore.KPIPeriodRow(f.ctx, f.q, f.scope(req), f.novPeriod())
	require.Error(t, err)
}

// filterFx is five finished tickets that differ in category, priority and group.
func filterFx(t *testing.T) *kpiFx {
	f := newKPIFx(t)
	fin := tk{resolved: tp(nov(10, 12, 0))}
	mk := func(cat *category.Category, p ticket.Priority, g *group.Group) {
		o := fin
		o.cat, o.prio, o.group = cat, p, g
		f.mk(o)
	}
	mk(&f.catA, ticket.PriorityHigh, &f.grp)
	mk(&f.catA, ticket.PriorityCritical, nil)
	mk(&f.catB, ticket.PriorityHigh, &f.grp2)
	mk(&f.catB, ticket.PriorityLow, nil)
	mk(&f.catB, ticket.PriorityMedium, &f.grp2)
	return f
}

func TestKPIPeriod_EveryFilterNarrows(t *testing.T) {
	cases := []struct {
		name string
		set  func(f *kpiFx, q *report.Query)
		want int64
	}{
		{"none", func(f *kpiFx, q *report.Query) {}, 5},
		{"empty priorities is no filter", func(f *kpiFx, q *report.Query) { q.Priorities = []ticket.Priority{} }, 5},
		{"category A", func(f *kpiFx, q *report.Query) { q.CategoryID = &f.catA.ID }, 2},
		{"category B", func(f *kpiFx, q *report.Query) { q.CategoryID = &f.catB.ID }, 3},
		{"one priority", func(f *kpiFx, q *report.Query) { q.Priorities = []ticket.Priority{ticket.PriorityHigh} }, 2},
		{"two priorities are ORed", func(f *kpiFx, q *report.Query) {
			q.Priorities = []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical}
		}, 3},
		{"group one", func(f *kpiFx, q *report.Query) { q.GroupID = &f.grp.ID }, 1},
		{"group two", func(f *kpiFx, q *report.Query) { q.GroupID = &f.grp2.ID }, 2},
		{"all three are ANDed", func(f *kpiFx, q *report.Query) {
			q.CategoryID, q.GroupID = &f.catB.ID, &f.grp2.ID
			q.Priorities = []ticket.Priority{ticket.PriorityHigh}
		}, 1},
		{"all three with no overlap", func(f *kpiFx, q *report.Query) {
			q.CategoryID, q.GroupID = &f.catA.ID, &f.grp2.ID
			q.Priorities = []ticket.Priority{ticket.PriorityHigh}
		}, 0},
		{"a category that does not exist matches nothing", func(f *kpiFx, q *report.Query) {
			q.CategoryID = ptr(uuid.New())
		}, 0},
		{"a group that does not exist matches nothing", func(f *kpiFx, q *report.Query) {
			q.GroupID = ptr(uuid.New())
		}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := filterFx(t)
			req := f.request()
			tc.set(f, &req.Query)
			got := f.period(req, f.novPeriod())
			require.Equal(t, tc.want, got.Created, "created")
			require.Equal(t, tc.want, got.Resolved, "resolved")
			if tc.want == 0 {
				require.Equal(t, report.PeriodRow{}, got, "an id that names nothing is zeros, not an error")
			}
		})
	}
}

func TestKPIPeriod_FiltersSeeTheCurrentValues(t *testing.T) {
	f := newKPIFx(t)
	tkt := f.mk(tk{cat: &f.catA, resolved: tp(nov(10, 12, 0))})
	_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET category_id = $2 WHERE id = $1`, tkt.ID, f.catB.ID)
	require.NoError(t, err)

	req := f.request()
	req.Query.CategoryID = &f.catA.ID
	require.EqualValues(t, 0, f.period(req, f.novPeriod()).Created)
	req.Query.CategoryID = &f.catB.ID
	require.EqualValues(t, 1, f.period(req, f.novPeriod()).Created)
}

// visFx is six finished tickets, each reachable by a different rule.
func visFx(t *testing.T) *kpiFx {
	f := newKPIFx(t)
	require.NoError(t, groupstore.New(f.q).AddScope(f.ctx, group.GroupScope{GroupID: f.grp.ID, CategoryID: f.catA.ID}))
	fin := tk{resolved: tp(nov(10, 12, 0))}
	mk := func(mod func(*tk)) {
		o := fin
		mod(&o)
		f.mk(o)
	}
	mk(func(o *tk) { o.cat = &f.catA })                        // the group's category
	mk(func(o *tk) { o.cat = &f.catB; o.assignee = &f.staff }) // assigned to Sam
	mk(func(o *tk) { o.cat = &f.catB; o.group = &f.grp })      // assigned to Sam's group
	mk(func(o *tk) { o.cat = &f.catB; o.reporter = &f.staff }) // reported by Sam
	mk(func(o *tk) { o.cat = &f.catB })                        // nothing links it to Sam
	mk(func(o *tk) { o.cat = &f.catB; o.reporter = &f.loner }) // reported by Lou
	return f
}

func TestKPIPeriod_Visibility(t *testing.T) {
	cases := []struct {
		name  string
		actor func(f *kpiFx) uuid.UUID
		vis   ticket.Visibility
		want  int64
	}{
		{"an administrator sees every ticket", func(f *kpiFx) uuid.UUID { return f.admin.ID }, ticket.VisibilityAll, 6},
		{"staff with scope enforcement off see every ticket", func(f *kpiFx) uuid.UUID { return f.staff.ID }, ticket.VisibilityAll, 6},
		{"scoped staff see their group's category, their assignments, their group and what they reported",
			func(f *kpiFx) uuid.UUID { return f.staff.ID }, ticket.VisibilityScoped, 4},
		{"scoped staff in no group see only what they reported",
			func(f *kpiFx) uuid.UUID { return f.loner.ID }, ticket.VisibilityScoped, 1},
		{"a reporter sees what they reported", func(f *kpiFx) uuid.UUID { return f.rep.ID }, ticket.VisibilityReporter, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := visFx(t)
			req := f.request()
			req.ActorID, req.Visibility = tc.actor(f), tc.vis
			got := f.period(req, f.novPeriod())
			require.Equal(t, tc.want, got.Created, "created")
			require.Equal(t, tc.want, got.Resolved, "resolved")

			cats, err := ticketstore.KPICreatedByCategoryRows(f.ctx, f.q, f.scope(req), f.novPeriod())
			require.NoError(t, err)
			var n int64
			for _, c := range cats {
				n += c.Count
			}
			require.Equal(t, tc.want, n, "the category breakdown is scoped the same way")
			prios, err := ticketstore.KPICreatedByPriorityRows(f.ctx, f.q, f.scope(req), f.novPeriod())
			require.NoError(t, err)
			n = 0
			for _, p := range prios {
				n += p.Count
			}
			require.Equal(t, tc.want, n, "the priority breakdown is scoped the same way")
		})
	}
}

func TestKPICreatedByCategory(t *testing.T) {
	f := newKPIFx(t)
	catC := f.mkCategory("Charlie")
	two := f.mkCategory("Alpha2") // ties with Alpha on count; ordered by name
	for i := 0; i < 3; i++ {
		f.mk(tk{cat: &catC})
	}
	f.mk(tk{cat: &f.catB})
	f.mk(tk{cat: &f.catB})
	f.mk(tk{cat: &two, resolved: tp(nov(10, 12, 0))}) // finished or not, it was created
	f.mk(tk{cat: &two})
	f.mk(tk{cat: &f.catA, created: utc(2026, time.October, 31, 23, 59)}) // before the period
	f.mk(tk{cat: &f.catA, created: utc(2026, time.December, 1, 0, 0)})   // after it

	got, err := ticketstore.KPICreatedByCategoryRows(f.ctx, f.q, f.scope(f.request()), f.novPeriod())
	require.NoError(t, err)
	names := make([]string, len(got))
	counts := make([]int64, len(got))
	for i, r := range got {
		names[i], counts[i] = r.Name, r.Count
	}
	require.Equal(t, []string{catC.Name, two.Name, f.catB.Name}, names, "count descending, then name; categories with no ticket are not rows")
	require.Equal(t, []int64{3, 2, 2}, counts)
	require.Equal(t, catC.ID, got[0].ID)

	req := f.request()
	req.Query.Priorities = []ticket.Priority{ticket.PriorityHigh}
	got, err = ticketstore.KPICreatedByCategoryRows(f.ctx, f.q, f.scope(req), f.novPeriod())
	require.NoError(t, err)
	require.Empty(t, got, "the filters narrow it")
}

func TestKPICreatedByCategory_NamesAreCurrentAndArchivedCategoriesCount(t *testing.T) {
	f := newKPIFx(t)
	f.mk(tk{cat: &f.catA})
	_, err := f.tx.ExecContext(f.ctx, `UPDATE categories SET name = $2, active = false WHERE id = $1`, f.catA.ID, "Renamed and archived")
	require.NoError(t, err)
	got, err := ticketstore.KPICreatedByCategoryRows(f.ctx, f.q, f.scope(f.request()), f.novPeriod())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Renamed and archived", got[0].Name)
}

func TestKPICreatedByPriority(t *testing.T) {
	f := newKPIFx(t)
	for i := 0; i < 3; i++ {
		f.mk(tk{prio: ticket.PriorityMedium})
	}
	f.mk(tk{prio: ticket.PriorityCritical})
	f.mk(tk{prio: ticket.PriorityLow, resolved: tp(nov(10, 12, 0))})
	f.mk(tk{prio: ticket.PriorityLow})
	f.mk(tk{prio: ticket.PriorityHigh, created: utc(2026, time.October, 31, 23, 59)}) // outside

	got, err := ticketstore.KPICreatedByPriorityRows(f.ctx, f.q, f.scope(f.request()), f.novPeriod())
	require.NoError(t, err)
	by := map[ticket.Priority]int64{}
	for _, r := range got {
		by[r.Priority] = r.Count
	}
	require.Equal(t, map[ticket.Priority]int64{
		ticket.PriorityMedium: 3, ticket.PriorityCritical: 1, ticket.PriorityLow: 2,
	}, by, "only priorities with tickets are rows; Go supplies the zeros and the order")

	req := f.request()
	req.Query.CategoryID = ptr(uuid.New())
	got, err = ticketstore.KPICreatedByPriorityRows(f.ctx, f.q, f.scope(req), f.novPeriod())
	require.NoError(t, err)
	require.Empty(t, got)
}
