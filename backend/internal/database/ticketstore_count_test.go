package database_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// CountFiltered is ListTicketsFiltered's WHERE with count(*) for a select list
// (#675), so the only thing worth testing against Postgres is that it counts
// what the list would return: for every kind of viewer, over every kind of
// ticket, under each of the filters a dashboard cell can set.
func TestCountFiltered_AgreesWithTheList(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	gs := groupstore.New(dbgen.New(f.tx))

	// f.scoped's coverage: all of catB, one type of catA (typeA), and the group
	// g2 they belong to, which holds some tickets.
	g2 := group.Group{ID: uuid.New(), Name: "CountGroup"}
	require.NoError(t, gs.Create(f.ctx, g2))
	require.NoError(t, gs.AddMember(f.ctx, g2.ID, f.scoped.ID))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catB.ID}))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catA.ID, TypeID: &f.typeA.ID}))

	// SLA: response target 100 min. An outstanding ticket 200 min old is
	// breached, one 90 min old is at risk, one 10 min old is neither.
	pol := sla.Policy{ID: uuid.New(), Name: "count", ResponseTargetMin: 100, ResolutionTargetMin: 100000}
	require.NoError(t, f.sls.CreatePolicy(f.ctx, pol))
	aged := func(subject string, age time.Duration, s spec) {
		s.subject, s.created, s.updated = subject, now.Add(-age), now.Add(-age)
		tk := f.mk(s)
		require.NoError(t, f.sls.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: pol.ID}))
	}

	// Every visibility case for f.scoped, and the one that is out of scope.
	aged("reported by me", 200*time.Minute, spec{reporter: &f.scoped, cat: &f.catA, typ: &f.typeB})
	aged("assigned to me", 90*time.Minute, spec{cat: &f.catA, typ: &f.typeB, assignee: &f.scoped})
	aged("in my group", 10*time.Minute, spec{cat: &f.catA, typ: &f.typeB, group: &g2})
	aged("covered category", 200*time.Minute, spec{cat: &f.catB, assignee: &f.staff})
	aged("covered type", 90*time.Minute, spec{cat: &f.catA, typ: &f.typeA, status: &f.stInProgress})
	aged("covered category, closed", 10*time.Minute, spec{cat: &f.catB, status: &f.stClosed})
	aged("out of scope", 200*time.Minute, spec{cat: &f.catA, typ: &f.typeB, assignee: &f.staff})
	// Every assignment case: nobody (the ones above with neither), a user, a
	// group, and both.
	aged("held by both", 10*time.Minute, spec{cat: &f.catB, assignee: &f.scoped, group: &g2})
	aged("someone else's, resolved", 10*time.Minute, spec{cat: &f.catB, assignee: &f.staff, status: &f.stResolved})

	admin := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll}
	staffUnscoped := ticket.Filter{ActorID: f.scoped.ID, Visibility: ticket.VisibilityAll}
	scoped := ticket.Filter{ActorID: f.scoped.ID, Visibility: ticket.VisibilityScoped}
	requester := ticket.Filter{ActorID: f.bob.ID, Visibility: ticket.VisibilityReporter}
	actors := []struct {
		name string
		base ticket.Filter
	}{{"admin", admin}, {"unscoped staff", staffUnscoped}, {"scoped staff", scoped}, {"requester", requester}}

	// The scoped answer is hand-derived, so the agreement below is not two
	// copies of the same mistake.
	want := []string{
		"alpha", "charlie", "Delta", "reported by me", "assigned to me", "in my group", "covered category",
		"covered type", "covered category, closed", "held by both", "someone else's, resolved",
	}
	slices.Sort(want)
	got, err := f.ts.ListFilteredAt(f.ctx, withLimit(scoped), now)
	require.NoError(t, err)
	var names []string
	for _, tk := range got {
		names = append(names, tk.Subject)
	}
	slices.Sort(names)
	require.Equal(t, want, names)
	n, err := f.ts.CountFilteredAt(f.ctx, scoped, now)
	require.NoError(t, err)
	require.EqualValues(t, len(want), n)

	filters := []struct {
		name  string
		apply func(*ticket.Filter)
	}{
		{"none", func(*ticket.Filter) {}},
		{"status", func(x *ticket.Filter) { x.StatusIDs = []uuid.UUID{f.stResolved.ID} }},
		{"two statuses", func(x *ticket.Filter) { x.StatusIDs = []uuid.UUID{f.stNew.ID, f.stInProgress.ID} }},
		{"include_closed=false", func(x *ticket.Filter) { x.ExcludeClosed = true }},
		{"sla breached", func(x *ticket.Filter) { x.SLA = ticket.SLABreached }},
		{"sla at risk", func(x *ticket.Filter) { x.SLA = ticket.SLAAtRisk }},
		{"sla breached, open only", func(x *ticket.Filter) { x.SLA, x.ExcludeClosed = ticket.SLABreached, true }},
		{"assignee user", func(x *ticket.Filter) { x.AssigneeUserID = &f.scoped.ID }},
		{"assignee group", func(x *ticket.Filter) { x.AssigneeGroupID = &g2.ID }},
		{"held by me", func(x *ticket.Filter) { x.HeldBy = ticket.HeldMe }},
		{"held by nobody", func(x *ticket.Filter) { x.HeldBy = ticket.HeldNobody }},
		{"held by others", func(x *ticket.Filter) { x.HeldBy = ticket.HeldOthers }},
		{"queue mine", func(x *ticket.Filter) { x.Queue = ticket.QueueMine }},
		{"queue assigned to me", func(x *ticket.Filter) { x.Queue = ticket.QueueAssignedToMe }},
		{"queue my groups", func(x *ticket.Filter) { x.Queue = ticket.QueueMyGroups }},
		{"queue unassigned", func(x *ticket.Filter) { x.Queue = ticket.QueueUnassigned }},
		{"priority and category", func(x *ticket.Filter) {
			x.Priorities, x.CategoryID = []ticket.Priority{ticket.PriorityMedium}, &f.catB.ID
		}},
		{"text", func(x *ticket.Filter) { x.Query = "covered" }},
		{"text with reply search asked for", func(x *ticket.Filter) { x.Query, x.SearchReplies = "covered", true }},
		{"created window", func(x *ticket.Filter) { x.CreatedAfter, x.CreatedBefore = ref(now.Add(-120*time.Minute)), ref(now) }},
		{"all at once", func(x *ticket.Filter) {
			x.ExcludeClosed, x.Queue, x.HeldBy, x.SLA = true, ticket.QueueMine, ticket.HeldMe, ticket.SLAAtRisk
		}},
	}

	seen := map[string]bool{}
	for _, a := range actors {
		for _, fl := range filters {
			x := a.base
			fl.apply(&x)
			list, err := f.ts.ListFilteredAt(f.ctx, withLimit(x), now)
			require.NoError(t, err)
			n, err := f.ts.CountFilteredAt(f.ctx, x, now)
			require.NoError(t, err)
			require.EqualValues(t, len(list), n, "%s, filter %s", a.name, fl.name)
			if n > 0 {
				seen[a.name+"/"+fl.name] = true
			}

			// Sort, limit and offset belong to the list; the count ignores them.
			y := x
			y.Sort, y.Order, y.Limit, y.Offset = ticket.SortPriority, ticket.OrderDesc, 1, 2
			m, err := f.ts.CountFilteredAt(f.ctx, y, now)
			require.NoError(t, err)
			require.Equal(t, n, m, "%s, filter %s: paging and sort must not change a count", a.name, fl.name)
		}
	}
	// Guard the guard: a test whose every count is zero agrees with anything.
	for _, k := range []string{"admin/none", "unscoped staff/none", "scoped staff/none", "requester/none",
		"scoped staff/sla breached", "scoped staff/sla at risk", "scoped staff/held by me", "scoped staff/queue my groups",
		"scoped staff/include_closed=false", "admin/held by others", "admin/held by nobody", "scoped staff/assignee group"} {
		require.True(t, seen[k], "%s counted nothing, so it proved nothing", k)
	}

	// The reply arm never runs in a count: asking for it changes nothing.
	a, err := f.ts.CountFilteredAt(f.ctx, withText(scoped, "covered", false), now)
	require.NoError(t, err)
	b, err := f.ts.CountFilteredAt(f.ctx, withText(scoped, "covered", true), now)
	require.NoError(t, err)
	require.Equal(t, a, b)

	// CountFiltered is the same at the wall clock.
	c, err := f.ts.CountFiltered(f.ctx, admin)
	require.NoError(t, err)
	l, err := f.ts.ListFiltered(f.ctx, withLimit(admin))
	require.NoError(t, err)
	require.EqualValues(t, len(l), c)
}

func withLimit(f ticket.Filter) ticket.Filter {
	f.Limit = 1000
	return f
}

func withText(f ticket.Filter, q string, replies bool) ticket.Filter {
	f.Query, f.SearchReplies = q, replies
	return f
}
