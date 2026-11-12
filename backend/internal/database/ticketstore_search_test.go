package database_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The advanced-search query is tested against Postgres for the same reason
// ListFiltered always was: a fake store would re-implement the predicate being
// verified. Every test here runs in a rolled-back transaction.

var searchT0 = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

func day(n int) time.Time { return searchT0.AddDate(0, 0, n) }

type searchFixture struct {
	t   *testing.T
	ctx context.Context
	tx  *sql.Tx
	ts  *ticketstore.Store
	us  *userstore.Store
	sls *slastore.Store

	staff, scoped               user.User
	dana, bob, gone, disabled   user.User
	catA, catB                  category.Category
	typeA, typeB                category.Type
	itemA                       category.Item
	group                       group.Group
	stNew, stResolved, stClosed ticket.Status
	stInProgress, stPending     ticket.Status
	stAck                       ticket.Status // sort_order 1, same as New, named before it
	seq                         int64
	byName                      map[string]ticket.Ticket
}

// spec is one ticket. Zero fields take the fixture's defaults.
type spec struct {
	subject   string
	seq       int64 // tracking sequence; 0 takes the next
	reporter  *user.User
	guestName string
	guestMail string
	cat       *category.Category
	typ       *category.Type
	item      *category.Item
	prio      ticket.Priority
	status    *ticket.Status
	assignee  *user.User
	group     *group.Group
	channel   ticket.SourceChannel
	created   time.Time
	updated   time.Time
	desc      string
}

func newSearchFixture(t *testing.T) *searchFixture {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)

	ctx := context.Background()
	f := &searchFixture{
		t: t, ctx: ctx, tx: tx,
		ts: ticketstore.New(q), us: userstore.New(q), sls: slastore.New(q),
		byName: map[string]ticket.Ticket{},
	}
	cs := categorystore.New(q)
	gs := groupstore.New(q)

	mkUser := func(name, email string, role user.Role) user.User {
		u := user.User{ID: uuid.New(), Email: email, DisplayName: name, Role: role,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, f.us.Create(ctx, u))
		return u
	}
	f.staff = mkUser("Sam Staff", "sam.staff@example.com", user.RoleStaff)
	f.scoped = mkUser("Sue Scoped", "sue.scoped@example.com", user.RoleStaff)
	f.dana = mkUser("Dana Whitfield", "dana.whitfield@example.com", user.RoleUser)
	f.bob = mkUser("Bob Brown", "bob.brown@example.com", user.RoleUser)
	f.gone = mkUser("Gone Goner", "gone@example.com", user.RoleUser)
	f.disabled = mkUser("Dis Abled", "dis.abled@example.com", user.RoleUser)
	require.NoError(t, f.us.SoftDelete(ctx, f.gone.ID))
	require.NoError(t, f.us.Disable(ctx, f.disabled.ID))

	f.catA = category.Category{ID: uuid.New(), Name: "SrchA", SortOrder: 1, Active: true}
	f.catB = category.Category{ID: uuid.New(), Name: "SrchB", SortOrder: 2, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, f.catA))
	require.NoError(t, cs.CreateCategory(ctx, f.catB))
	f.typeA = category.Type{ID: uuid.New(), CategoryID: f.catA.ID, Name: "TA", SortOrder: 1, Active: true}
	f.typeB = category.Type{ID: uuid.New(), CategoryID: f.catA.ID, Name: "TB", SortOrder: 2, Active: true}
	require.NoError(t, cs.CreateType(ctx, f.typeA))
	require.NoError(t, cs.CreateType(ctx, f.typeB))
	f.itemA = category.Item{ID: uuid.New(), TypeID: f.typeA.ID, Name: "IA", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateItem(ctx, f.itemA))

	f.group = group.Group{ID: uuid.New(), Name: "SrchGroup"}
	require.NoError(t, gs.Create(ctx, f.group))
	require.NoError(t, gs.AddMember(ctx, f.group.ID, f.staff.ID))

	status := func(name string) ticket.Status {
		st, err := f.ts.GetStatusByName(ctx, name)
		require.NoError(t, err)
		return st
	}
	f.stNew, f.stResolved, f.stClosed = status(ticket.StatusNameNew), status(ticket.StatusNameResolved), status(ticket.StatusNameClosed)
	f.stInProgress, f.stPending = status("In Progress"), status("Pending")
	f.stAck = ticket.Status{ID: uuid.New(), Name: "Acknowledged", Kind: ticket.StatusKindCustom, SortOrder: 1, Color: "#111111", Active: true}
	require.NoError(t, f.ts.CreateStatus(ctx, f.stAck))
	f.seq = 1000 // clear of the fixture's explicit 101-106

	// The six fixture tickets. Subjects mix case, tracking sequences are not in
	// creation order and the statuses and priorities tie, so every sort test
	// can tell its column from a lucky default.
	f.mk(spec{subject: "alpha", seq: 103, reporter: &f.dana, cat: &f.catA, typ: &f.typeA, item: &f.itemA,
		prio: ticket.PriorityHigh, status: &f.stNew, assignee: &f.staff, channel: ticket.ChannelWeb,
		created: day(1), updated: day(10)})
	f.mk(spec{subject: "Bravo", seq: 101, reporter: &f.bob, cat: &f.catA, typ: &f.typeB,
		prio: ticket.PriorityLow, status: &f.stResolved, group: &f.group, channel: ticket.ChannelAPI,
		created: day(2), updated: day(9)})
	f.mk(spec{subject: "charlie", seq: 106, reporter: &f.gone, cat: &f.catB,
		prio: ticket.PriorityMedium, status: &f.stClosed, channel: ticket.ChannelMCP,
		created: day(3), updated: day(8)})
	f.mk(spec{subject: "Delta", seq: 102, guestName: "Gwen Guest", guestMail: "gwen@guest.example", cat: &f.catB,
		prio: ticket.PriorityCritical, status: &f.stAck, channel: ticket.ChannelGuest,
		created: day(4), updated: day(7)})
	f.mk(spec{subject: "echo", seq: 105, reporter: &f.disabled, cat: &f.catA,
		prio: ticket.PriorityMedium, status: &f.stInProgress, channel: ticket.ChannelPhone,
		created: day(5), updated: day(6)})
	// The one place a % and an _ appear in any name, for the literal-match test.
	f.mk(spec{subject: "Foxtrot", seq: 104, reporter: &f.dana, guestName: "100%_sure", cat: &f.catA,
		prio: ticket.PriorityMedium, status: &f.stPending, channel: ticket.ChannelWeb,
		created: day(6), updated: day(5)})
	return f
}

func (f *searchFixture) mk(s spec) ticket.Ticket {
	f.t.Helper()
	if s.seq == 0 {
		f.seq++
		s.seq = f.seq
	}
	if s.reporter == nil && s.guestMail == "" {
		s.reporter = &f.bob // a ticket needs a reporter or a guest address
	}
	if s.cat == nil {
		s.cat = &f.catA
	}
	if s.prio == "" {
		s.prio = ticket.PriorityMedium
	}
	if s.status == nil {
		s.status = &f.stNew
	}
	if s.channel == "" {
		s.channel = ticket.ChannelWeb
	}
	if s.created.IsZero() {
		s.created = day(20)
	}
	if s.updated.IsZero() {
		s.updated = s.created
	}
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, s.seq),
		Subject: s.subject, Description: s.desc + " body", DescriptionFormat: ticket.TextFormatPlain,
		SourceChannel: s.channel, CategoryID: s.cat.ID, Priority: s.prio, StatusID: s.status.ID,
		GuestName: s.guestName, CreatedAt: s.created, UpdatedAt: s.updated,
	}
	if s.typ != nil {
		tk.TypeID = &s.typ.ID
	}
	if s.item != nil {
		tk.ItemID = &s.item.ID
	}
	if s.reporter != nil {
		tk.ReporterUserID = &s.reporter.ID
	}
	if s.guestMail != "" {
		tk.GuestEmail = &s.guestMail
	}
	if s.assignee != nil {
		tk.AssigneeUserID = &s.assignee.ID
	}
	if s.group != nil {
		tk.AssigneeGroupID = &s.group.ID
	}
	require.NoError(f.t, f.ts.Create(f.ctx, tk))
	f.byName[s.subject] = tk
	return tk
}

// all is the filter every test starts from: an administrator, no restriction.
func (f *searchFixture) all() ticket.Filter {
	return ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 200}
}

func (f *searchFixture) list(fl ticket.Filter) []string {
	f.t.Helper()
	got, err := f.ts.ListFiltered(f.ctx, fl)
	require.NoError(f.t, err)
	out := make([]string, len(got))
	for i, tk := range got {
		out[i] = tk.Subject
	}
	return out
}

func (f *searchFixture) set(fl ticket.Filter) []string {
	out := f.list(fl)
	slices.Sort(out)
	return out
}

var fixtureSubjects = []string{"Bravo", "Delta", "Foxtrot", "alpha", "charlie", "echo"} // sorted

func ref[T any](v T) *T { return &v }

func TestListFiltered_EveryFilterNarrows(t *testing.T) {
	f := newSearchFixture(t)

	tests := []struct {
		name string
		mod  func(*ticket.Filter)
		want []string
	}{
		{"reporter_id", func(x *ticket.Filter) { x.ReporterID = &f.dana.ID }, []string{"Foxtrot", "alpha"}},
		{"reporter_id of a deleted account", func(x *ticket.Filter) { x.ReporterID = &f.gone.ID }, []string{"charlie"}},
		{"reporter_id nobody has", func(x *ticket.Filter) { x.ReporterID = ref(uuid.New()) }, []string{}},
		{"created_after is inclusive", func(x *ticket.Filter) { x.CreatedAfter = ref(day(5)) }, []string{"Foxtrot", "echo"}},
		{"created_before is exclusive", func(x *ticket.Filter) { x.CreatedBefore = ref(day(3)) }, []string{"Bravo", "alpha"}},
		{"created range", func(x *ticket.Filter) { x.CreatedAfter, x.CreatedBefore = ref(day(2)), ref(day(4)) }, []string{"Bravo", "charlie"}},
		{"updated_after is inclusive", func(x *ticket.Filter) { x.UpdatedAfter = ref(day(9)) }, []string{"Bravo", "alpha"}},
		{"updated_before is exclusive", func(x *ticket.Filter) { x.UpdatedBefore = ref(day(7)) }, []string{"Foxtrot", "echo"}},
		{"updated range", func(x *ticket.Filter) { x.UpdatedAfter, x.UpdatedBefore = ref(day(6)), ref(day(9)) }, []string{"Delta", "charlie", "echo"}},
		{"one priority", func(x *ticket.Filter) { x.Priorities = []ticket.Priority{ticket.PriorityHigh} }, []string{"alpha"}},
		{"priorities are ORed", func(x *ticket.Filter) {
			x.Priorities = []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical}
		}, []string{"Delta", "alpha"}},
		{"category", func(x *ticket.Filter) { x.CategoryID = &f.catB.ID }, []string{"Delta", "charlie"}},
		{"type", func(x *ticket.Filter) { x.TypeID = &f.typeB.ID }, []string{"Bravo"}},
		{"item", func(x *ticket.Filter) { x.ItemID = &f.itemA.ID }, []string{"alpha"}},
		{"assignee person", func(x *ticket.Filter) { x.AssigneeUserID = &f.staff.ID }, []string{"alpha"}},
		{"assignee group", func(x *ticket.Filter) { x.AssigneeGroupID = &f.group.ID }, []string{"Bravo"}},
		{"status", func(x *ticket.Filter) { x.StatusIDs = []uuid.UUID{f.stNew.ID} }, []string{"alpha"}},
		{"statuses are ORed", func(x *ticket.Filter) { x.StatusIDs = []uuid.UUID{f.stResolved.ID, f.stClosed.ID} }, []string{"Bravo", "charlie"}},
		{"exclude closed", func(x *ticket.Filter) { x.ExcludeClosed = true }, []string{"Bravo", "Delta", "Foxtrot", "alpha", "echo"}},
		{"source channel", func(x *ticket.Filter) { x.SourceChannels = []ticket.SourceChannel{ticket.ChannelWeb} }, []string{"Foxtrot", "alpha"}},
		{"source channels are ORed", func(x *ticket.Filter) {
			x.SourceChannels = []ticket.SourceChannel{ticket.ChannelAPI, ticket.ChannelMCP, ticket.ChannelPhone}
		}, []string{"Bravo", "charlie", "echo"}},
		{"queue mine: assigned to me or to my group", func(x *ticket.Filter) { x.Queue = ticket.QueueMine }, []string{"Bravo", "alpha"}},
		{"queue unassigned", func(x *ticket.Filter) { x.Queue = ticket.QueueUnassigned }, []string{"Delta", "Foxtrot", "charlie", "echo"}},
		{"an unknown sla value matches nothing", func(x *ticket.Filter) { x.SLA = "bogus" }, []string{}},
		{"q still matches", func(x *ticket.Filter) { x.Query = "alpha" }, []string{"alpha"}},
		{"filters combine and only narrow", func(x *ticket.Filter) {
			x.CategoryID = &f.catA.ID
			x.Priorities = []ticket.Priority{ticket.PriorityMedium}
			x.CreatedAfter = ref(day(6))
		}, []string{"Foxtrot"}},

		// Requester free text: the account's name and email, and the ticket's
		// guest name and email.
		{"requester: account name", func(x *ticket.Filter) { x.Requester = "Whitfield" }, []string{"Foxtrot", "alpha"}},
		{"requester: account email", func(x *ticket.Filter) { x.Requester = "bob.brown@" }, []string{"Bravo"}},
		{"requester: guest name", func(x *ticket.Filter) { x.Requester = "Gwen" }, []string{"Delta"}},
		{"requester: guest email", func(x *ticket.Filter) { x.Requester = "guest.example" }, []string{"Delta"}},
		{"requester ignores case", func(x *ticket.Filter) { x.Requester = "dANA wHITFIELD" }, []string{"Foxtrot", "alpha"}},
		{"requester: a deleted account still matches", func(x *ticket.Filter) { x.Requester = "Goner" }, []string{"charlie"}},
		{"requester: a disabled account still matches", func(x *ticket.Filter) { x.Requester = "abled" }, []string{"echo"}},
		{"requester: % is literal", func(x *ticket.Filter) { x.Requester = "%" }, []string{"Foxtrot"}},
		{"requester: _ is literal", func(x *ticket.Filter) { x.Requester = "_" }, []string{"Foxtrot"}},
		{"requester: both literal", func(x *ticket.Filter) { x.Requester = "0%_s" }, []string{"Foxtrot"}},
		{"requester: backslash is literal", func(x *ticket.Filter) { x.Requester = `\` }, []string{}},
		{"requester: no match", func(x *ticket.Filter) { x.Requester = "nobody at all" }, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fl := f.all()
			tc.mod(&fl)
			require.Equal(t, tc.want, f.set(fl))
		})
	}
}

// A zero Filter has every slice nil. The store turns "absent" into an empty
// array, so no filter must not mean "match nothing". The default order is
// newest first, ties broken by id.
func TestListFiltered_NoFilterReturnsEveryTicketNewestFirst(t *testing.T) {
	f := newSearchFixture(t)
	want := make([]ticket.Ticket, 0, len(f.byName))
	for _, tk := range f.byName {
		want = append(want, tk)
	}
	slices.SortFunc(want, func(a, b ticket.Ticket) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	got, err := f.ts.ListFiltered(f.ctx, f.all())
	require.NoError(t, err)
	require.Equal(t, ids(want), ids(got))
	require.Len(t, got, len(fixtureSubjects))
}

func ids(ts []ticket.Ticket) []uuid.UUID {
	out := make([]uuid.UUID, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

// A filter can only narrow. For every ticket the actor cannot see, a filter
// built from that ticket's own attributes must not bring it back.
func TestListFiltered_FiltersNeverEscapeVisibility(t *testing.T) {
	f := newSearchFixture(t)
	// scoped sees exactly one ticket: "alpha", reported by dana, because they
	// are its assignee.
	alpha := f.byName["alpha"]
	reassigned := alpha
	reassigned.AssigneeUserID = &f.scoped.ID
	require.NoError(t, f.ts.Update(f.ctx, reassigned))

	scoped := ticket.Filter{ActorID: f.scoped.ID, Visibility: ticket.VisibilityScoped, Limit: 200}
	require.Equal(t, []string{"alpha"}, f.list(scoped), "precondition: only the assigned ticket is visible")

	reporter := ticket.Filter{ActorID: f.dana.ID, Visibility: ticket.VisibilityReporter, Limit: 200}
	require.Equal(t, []string{"Foxtrot", "alpha"}, f.set(reporter), "precondition: dana sees what she reported")

	requesterOf := func(tk ticket.Ticket) string {
		for _, u := range []user.User{f.dana, f.bob, f.gone, f.disabled} {
			if tk.ReporterUserID != nil && *tk.ReporterUserID == u.ID {
				return u.DisplayName
			}
		}
		return tk.GuestName
	}
	for name, tk := range f.byName {
		// One filter per attribute, each naming something the ticket owns.
		each := map[string]func(*ticket.Filter){
			"reporter":  func(x *ticket.Filter) { x.ReporterID = tk.ReporterUserID },
			"requester": func(x *ticket.Filter) { x.Requester = requesterOf(tk) },
			"assignee":  func(x *ticket.Filter) { x.AssigneeUserID = tk.AssigneeUserID },
			"group":     func(x *ticket.Filter) { x.AssigneeGroupID = tk.AssigneeGroupID },
			"category":  func(x *ticket.Filter) { x.CategoryID = &tk.CategoryID },
			"type":      func(x *ticket.Filter) { x.TypeID = tk.TypeID },
			"item":      func(x *ticket.Filter) { x.ItemID = tk.ItemID },
			"priority":  func(x *ticket.Filter) { x.Priorities = []ticket.Priority{tk.Priority} },
			"status":    func(x *ticket.Filter) { x.StatusIDs = []uuid.UUID{tk.StatusID} },
			"channel":   func(x *ticket.Filter) { x.SourceChannels = []ticket.SourceChannel{tk.SourceChannel} },
			"created": func(x *ticket.Filter) {
				x.CreatedAfter, x.CreatedBefore = ref(tk.CreatedAt), ref(tk.CreatedAt.Add(time.Second))
			},
			"updated": func(x *ticket.Filter) {
				x.UpdatedAfter, x.UpdatedBefore = ref(tk.UpdatedAt), ref(tk.UpdatedAt.Add(time.Second))
			},
			"query": func(x *ticket.Filter) { x.Query = tk.Subject },
			"sla":   func(x *ticket.Filter) { x.SLA = ticket.SLABreached },
			"all at once": func(x *ticket.Filter) {
				x.ReporterID, x.CategoryID, x.Priorities = tk.ReporterUserID, &tk.CategoryID, []ticket.Priority{tk.Priority}
				x.Queue = ticket.QueueAny
			},
		}
		for attr, apply := range each {
			fl := scoped
			apply(&fl)
			for _, s := range f.list(fl) {
				require.Equal(t, "alpha", s, "scoped actor, ticket %q, filter %s: out-of-scope ticket returned", name, attr)
			}
			fl = reporter
			apply(&fl)
			for _, s := range f.list(fl) {
				require.Contains(t, []string{"alpha", "Foxtrot"}, s, "reporter, ticket %q, filter %s: someone else's ticket returned", name, attr)
			}
		}
	}

	// A requester text that matches a name the actor cannot see is not an
	// oracle either: it narrows within what they can see, and that is all.
	fl := scoped
	fl.Requester = "Brown"
	require.Empty(t, f.list(fl))
	fl = scoped
	fl.Requester = "Whitfield"
	require.Equal(t, []string{"alpha"}, f.list(fl))
}

// The scoped-staff predicate has more arms than any other, so this holds it to
// a hand-derived answer over every kind of scope: a whole category, one type of
// a category, a group assignment, reporter and assignee.
func TestListFiltered_ScopedVisibility(t *testing.T) {
	f := newSearchFixture(t)
	q := dbgen.New(f.tx)
	gs := groupstore.New(q)
	g2 := group.Group{ID: uuid.New(), Name: "SrchGroup2"}
	require.NoError(t, gs.Create(f.ctx, g2))
	require.NoError(t, gs.AddMember(f.ctx, g2.ID, f.scoped.ID))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catB.ID}))                      // all of catB
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g2.ID, CategoryID: f.catA.ID, TypeID: &f.typeA.ID})) // one type of catA

	// A second group, and a second typed row in a different category. The
	// store passes the covered categories and types as two arrays that must stay
	// aligned by position, and one typed row cannot tell aligned from reversed.
	// The category and the type here are inactive: coverage does not look.
	cs := categorystore.New(q)
	catC := category.Category{ID: uuid.New(), Name: "SrchC", SortOrder: 3, Active: false}
	typeC := category.Type{ID: uuid.New(), CategoryID: catC.ID, Name: "TC", SortOrder: 1, Active: false}
	typeC2 := category.Type{ID: uuid.New(), CategoryID: catC.ID, Name: "TC2", SortOrder: 2, Active: true}
	require.NoError(t, cs.CreateCategory(f.ctx, catC))
	require.NoError(t, cs.CreateType(f.ctx, typeC))
	require.NoError(t, cs.CreateType(f.ctx, typeC2))
	g3 := group.Group{ID: uuid.New(), Name: "SrchGroup3"}
	require.NoError(t, gs.Create(f.ctx, g3))
	require.NoError(t, gs.AddMember(f.ctx, g3.ID, f.scoped.ID))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: g3.ID, CategoryID: catC.ID, TypeID: &typeC.ID}))
	f.mk(spec{subject: "C typeC", cat: &catC, typ: &typeC})
	f.mk(spec{subject: "C typeC2", cat: &catC, typ: &typeC2})
	f.mk(spec{subject: "C none", cat: &catC})

	f.mk(spec{subject: "A none", cat: &f.catA})
	f.mk(spec{subject: "A typeA", cat: &f.catA, typ: &f.typeA})
	f.mk(spec{subject: "A typeB", cat: &f.catA, typ: &f.typeB})
	f.mk(spec{subject: "B none", cat: &f.catB})
	// A ticket that names an item, under a category the actor is scoped to whole.
	// Items never factor in, so the item must not hide it.
	typeBItem := category.Type{ID: uuid.New(), CategoryID: f.catB.ID, Name: "TBI", SortOrder: 1, Active: true}
	itemB := category.Item{ID: uuid.New(), TypeID: typeBItem.ID, Name: "IB", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(f.ctx, typeBItem))
	require.NoError(t, cs.CreateItem(f.ctx, itemB))
	f.mk(spec{subject: "B with item", cat: &f.catB, typ: &typeBItem, item: &itemB})
	f.mk(spec{subject: "assigned to scoped", cat: &f.catA, typ: &f.typeB, assignee: &f.scoped})
	f.mk(spec{subject: "scoped's group", cat: &f.catA, typ: &f.typeB, group: &g2})
	f.mk(spec{subject: "reported by scoped", cat: &f.catA, typ: &f.typeB, reporter: &f.scoped})

	// staff is in the fixture's group, which has no scope, so sees what is
	// assigned to them or that group. dana is in no group and sees only what she
	// reported.
	staffSees := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityScoped, Limit: 500}
	require.Equal(t, []string{"Bravo", "alpha"}, f.set(staffSees))
	danaSees := ticket.Filter{ActorID: f.dana.ID, Visibility: ticket.VisibilityScoped, Limit: 500}
	require.Equal(t, []string{"Foxtrot", "alpha"}, f.set(danaSees))

	fl := ticket.Filter{ActorID: f.scoped.ID, Visibility: ticket.VisibilityScoped, Limit: 500}
	require.Equal(t, []string{"A typeA", "B none", "B with item", "C typeC", "Delta", "alpha", "assigned to scoped", "charlie", "reported by scoped", "scoped's group"}, f.set(fl))
}

// "Mine" is the caller's own assignments plus those of every group they are in,
// and nothing else: not a group they are not in, and not a group they left.
func TestListFiltered_QueueMine(t *testing.T) {
	f := newSearchFixture(t)
	gs := groupstore.New(dbgen.New(f.tx))
	other := group.Group{ID: uuid.New(), Name: "SrchOtherGroup"}
	second := group.Group{ID: uuid.New(), Name: "SrchSecondGroup"}
	require.NoError(t, gs.Create(f.ctx, other))
	require.NoError(t, gs.Create(f.ctx, second))
	require.NoError(t, gs.AddMember(f.ctx, second.ID, f.staff.ID))

	f.mk(spec{subject: "second group", group: &second})
	f.mk(spec{subject: "someone else's group", group: &other})
	f.mk(spec{subject: "someone else's person", assignee: &f.scoped})

	mine := func(u user.User) []string {
		fl := ticket.Filter{ActorID: u.ID, Visibility: ticket.VisibilityAll, Queue: ticket.QueueMine, Limit: 200}
		return f.set(fl)
	}
	require.Equal(t, []string{"Bravo", "alpha", "second group"}, mine(f.staff), "own, first group and second group")
	require.Equal(t, []string{"someone else's person"}, mine(f.scoped), "a user in no group sees only their own")
	require.Empty(t, mine(f.dana), "nothing assigned, no groups")

	require.NoError(t, gs.RemoveMember(f.ctx, second.ID, f.staff.ID))
	require.Equal(t, []string{"Bravo", "alpha"}, mine(f.staff), "a group they left no longer counts")
}

func TestListFiltered_PagesAreFullAndStable(t *testing.T) {
	f := newSearchFixture(t)
	// Eleven tickets with the very same created_at, priority and status: only
	// the id tie-break keeps their order the same from page to page.
	for i := 0; i < 11; i++ {
		f.mk(spec{subject: "tie", created: day(30), updated: day(30)})
	}
	for _, sortKey := range []ticket.SortKey{"", ticket.SortPriority, ticket.SortStatus, ticket.SortCreated, ticket.SortUpdated, ticket.SortSLA} {
		base := f.all()
		base.CategoryID = &f.catA.ID
		base.Sort = sortKey
		whole, err := f.ts.ListFiltered(f.ctx, base)
		require.NoError(t, err)
		require.Greater(t, len(whole), 12)

		var paged []uuid.UUID
		for off := 0; ; off += 4 {
			p := base
			p.Limit, p.Offset = 4, off
			page, err := f.ts.ListFiltered(f.ctx, p)
			require.NoError(t, err)
			if off+4 <= len(whole) {
				require.Len(t, page, 4, "sort %q offset %d: a filtered page must be full", sortKey, off)
			}
			paged = append(paged, ids(page)...)
			if len(page) < 4 {
				break
			}
		}
		require.Equal(t, ids(whole), paged, "sort %q: pages must tile the whole result with no repeat and no gap", sortKey)
	}
}

func TestListFiltered_Sort(t *testing.T) {
	f := newSearchFixture(t)
	tests := []struct {
		key   ticket.SortKey
		order ticket.SortOrder
		want  []string
	}{
		// tracking sequences: Bravo 101, Delta 102, alpha 103, Foxtrot 104, echo 105, charlie 106
		{ticket.SortNumber, ticket.OrderAsc, []string{"Bravo", "Delta", "alpha", "Foxtrot", "echo", "charlie"}},
		{ticket.SortNumber, ticket.OrderDesc, []string{"charlie", "echo", "Foxtrot", "alpha", "Delta", "Bravo"}},
		// lower(subject): a Bravo sorted as bytes would come before alpha
		{ticket.SortSubject, ticket.OrderAsc, []string{"alpha", "Bravo", "charlie", "Delta", "echo", "Foxtrot"}},
		{ticket.SortSubject, ticket.OrderDesc, []string{"Foxtrot", "echo", "Delta", "charlie", "Bravo", "alpha"}},
		// sort_order, then name: Acknowledged(1) New(1) In Progress(2) Pending(3) Resolved(98) Closed(99)
		{ticket.SortStatus, ticket.OrderAsc, []string{"Delta", "alpha", "echo", "Foxtrot", "Bravo", "charlie"}},
		{ticket.SortStatus, ticket.OrderDesc, []string{"charlie", "Bravo", "Foxtrot", "echo", "alpha", "Delta"}},
		// critical high medium low; medium ties fall to created_at DESC in BOTH directions
		{ticket.SortPriority, ticket.OrderAsc, []string{"Delta", "alpha", "Foxtrot", "echo", "charlie", "Bravo"}},
		{ticket.SortPriority, ticket.OrderDesc, []string{"Bravo", "Foxtrot", "echo", "charlie", "alpha", "Delta"}},
		{ticket.SortCreated, ticket.OrderAsc, []string{"alpha", "Bravo", "charlie", "Delta", "echo", "Foxtrot"}},
		{ticket.SortCreated, ticket.OrderDesc, []string{"Foxtrot", "echo", "Delta", "charlie", "Bravo", "alpha"}},
		{ticket.SortUpdated, ticket.OrderAsc, []string{"Foxtrot", "echo", "Delta", "charlie", "Bravo", "alpha"}},
		{ticket.SortUpdated, ticket.OrderDesc, []string{"alpha", "Bravo", "charlie", "Delta", "echo", "Foxtrot"}},
	}
	for _, tc := range tests {
		t.Run(string(tc.key)+" "+string(tc.order), func(t *testing.T) {
			fl := f.all()
			fl.Sort, fl.Order = tc.key, tc.order
			require.Equal(t, tc.want, f.list(fl))
		})
	}

	t.Run("no sort is newest first", func(t *testing.T) {
		require.Equal(t, []string{"Foxtrot", "echo", "Delta", "charlie", "Bravo", "alpha"}, f.list(f.all()))
	})
	t.Run("a column's natural direction when order is absent", func(t *testing.T) {
		fl := f.all()
		fl.Sort = ticket.SortNumber
		require.Equal(t, []string{"Bravo", "Delta", "alpha", "Foxtrot", "echo", "charlie"}, f.list(fl))
		fl.Sort = ticket.SortUpdated
		require.Equal(t, []string{"alpha", "Bravo", "charlie", "Delta", "echo", "Foxtrot"}, f.list(fl))
	})
	t.Run("an order without a sort is ignored", func(t *testing.T) {
		fl := f.all()
		fl.Order = ticket.OrderAsc
		require.Equal(t, []string{"Foxtrot", "echo", "Delta", "charlie", "Bravo", "alpha"}, f.list(fl))
	})
	t.Run("a sort composes with filters and visibility", func(t *testing.T) {
		fl := f.all()
		fl.CategoryID = &f.catA.ID
		fl.Sort, fl.Order = ticket.SortSubject, ticket.OrderDesc
		require.Equal(t, []string{"Foxtrot", "echo", "Bravo", "alpha"}, f.list(fl))
	})
}

func TestListFiltered_RelevanceUntilAColumnIsChosen(t *testing.T) {
	f := newSearchFixture(t)
	// The better match is the OLDER ticket, so relevance and "newest first"
	// disagree.
	f.mk(spec{subject: "zebra zebra zebra", created: day(40)})
	f.mk(spec{subject: "zebra", created: day(41)})

	fl := f.all()
	fl.Query = "zebra"
	require.Equal(t, []string{"zebra zebra zebra", "zebra"}, f.list(fl), "a text search with no sort is ranked")

	fl.Sort, fl.Order = ticket.SortCreated, ticket.OrderDesc
	require.Equal(t, []string{"zebra", "zebra zebra zebra"}, f.list(fl), "a chosen column replaces relevance; the text only narrows")
}

// The SLA filter and sort must put every ticket in the bucket the list's
// indicator shows. The indicator is sla.StatusFor, so that is the oracle: each
// ticket below is read back through the real store and judged by it.
func TestListFiltered_SLAAgreesWithStatusFor(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	const respMin, resMin = 100, 1000 // 6000 s and 60000 s

	pol := sla.Policy{ID: uuid.New(), Name: "agree", ResponseTargetMin: respMin, ResolutionTargetMin: resMin}
	require.NoError(t, f.sls.CreatePolicy(f.ctx, pol))

	type scenario struct {
		name string
		// age is how long the clock has run when nothing paused it.
		age time.Duration
		// mutate adjusts the ticket and record before they are stored.
		mutate func(tk *ticket.Ticket)
		// record adjusts the SQL-only columns the store has no setter for.
		record string
		args   func(tk ticket.Ticket) []any
		// noRecord leaves the ticket outside any SLA.
		noRecord bool
	}
	s := func(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }
	resp := func(pct float64) time.Duration { return s(6000 * pct / 100) }

	scenarios := []scenario{
		{name: "response 50%", age: resp(50)},
		{name: "response 79.9%", age: resp(79.9)},
		{name: "response 1us under 80%", age: resp(80) - time.Microsecond},
		{name: "response exactly 80%", age: resp(80)},
		{name: "response 99.9%", age: resp(99.9)},
		{name: "response 1us under 100%", age: resp(100) - time.Microsecond},
		{name: "response exactly 100%", age: resp(100)},
		{name: "response 150%", age: resp(150)},
		// resolution target is 60000 s: 80% is 48000 s
		{name: "resolution 79.9% (response met)", age: s(47940), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 10`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(10 * time.Second)} }},
		{name: "resolution exactly 80% (response met)", age: s(48000), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 10`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(10 * time.Second)} }},
		{name: "resolution exactly 100% (response met)", age: s(60000), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 10`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(10 * time.Second)} }},

		// Pending: the clock stopped at pending_since.
		{name: "pending, frozen at 50%", age: resp(500), mutate: func(tk *ticket.Ticket) {
			tk.PendingSince = ref(tk.CreatedAt.Add(resp(50)))
		}},
		{name: "pending, frozen at 85%", age: resp(500), mutate: func(tk *ticket.Ticket) {
			tk.PendingSince = ref(tk.CreatedAt.Add(resp(85)))
		}},
		{name: "pending, frozen at 100%", age: resp(500), mutate: func(tk *ticket.Ticket) {
			tk.PendingSince = ref(tk.CreatedAt.Add(resp(100)))
		}},
		{name: "pending set after now is ignored", age: resp(90), mutate: func(tk *ticket.Ticket) {
			tk.PendingSince = ref(now.Add(time.Hour))
		}},
		// Closed pending intervals are subtracted.
		{name: "aged 150% with 60% paused", age: resp(150), mutate: func(tk *ticket.Ticket) { tk.SLAPausedSeconds = 3600 }},
		{name: "aged 150% with 80% paused", age: resp(150), mutate: func(tk *ticket.Ticket) { tk.SLAPausedSeconds = 4800 }},
		{name: "paused and pending together", age: resp(300), mutate: func(tk *ticket.Ticket) {
			tk.SLAPausedSeconds = 1200
			tk.PendingSince = ref(tk.CreatedAt.Add(resp(130))) // 130% - 20% paused = 110%
		}},

		// A restarted clock: the record starts later and counts pauses from then.
		{name: "restarted at 50% of a new clock", age: resp(300), record: `started_at = $2::timestamptz, paused_seconds_at_start = 0`,
			args: func(tk ticket.Ticket) []any { return []any{now.Add(-resp(50))} }},
		{name: "restarted, new clock at 90%, pauses before it ignored", age: resp(300), mutate: func(tk *ticket.Ticket) { tk.SLAPausedSeconds = 2000 },
			record: `started_at = $2::timestamptz, paused_seconds_at_start = 2000`,
			args:   func(tk ticket.Ticket) []any { return []any{now.Add(-resp(90))} }},

		// Met targets keep their frozen reading, however long ago.
		{name: "response met on time, ticket old", age: resp(500), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 600, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 600`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(10 * time.Minute)} }},
		{name: "response met late, frozen 150%", age: resp(500), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 9000, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 9000`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(150 * time.Minute)} }},
		{name: "response met in the amber band, frozen 85%", age: resp(500), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 5100, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 5100`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(85 * time.Minute)} }},
		{name: "response met, frozen exactly 80%", age: resp(500), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 4800, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 100`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(80 * time.Minute)} }},
		{name: "response met, frozen exactly 100%", age: resp(500), record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 6000, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 100`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(100 * time.Minute)} }},
		// Met, then paused afterwards: the ticket's pause total keeps growing for
		// the rest of its life, and recomputing a met target against it would
		// subtract pause time that had not happened yet when it was met. Only the
		// frozen reading is right, so an implementation that ignores it reads
		// green here (9000 s - 6000 s paused = 50%) where StatusFor reads red.
		{name: "met late (frozen 150%), paused afterwards", age: resp(500),
			mutate: func(tk *ticket.Ticket) {
				tk.SLAPausedSeconds = 6000
				tk.PendingSince = ref(now.Add(-time.Hour))
			},
			record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 9000, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 9000`,
			args:   func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(150 * time.Minute)} }},
		{name: "met in the amber band (frozen 85%), paused afterwards", age: resp(500),
			mutate: func(tk *ticket.Ticket) { tk.SLAPausedSeconds = 3000 },
			record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 5100, resolved_at = $2::timestamptz, resolution_elapsed_at_met_seconds = 5100`,
			args:   func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(85 * time.Minute)} }},
		// The same for the RESOLUTION target, which the cases above only exercise
		// on the response side. Resolved late (72000 s of a 60000 s target, 120%),
		// the ticket then paused for 50000 s: recomputing the met target against
		// the pause total would read 22000 s, 37%, green. The frozen reading is
		// red, and so is StatusFor. The response was met on time, so the red
		// bucket comes from the resolution alone.
		{name: "resolved late (frozen 120%), paused afterwards", age: s(200000),
			mutate: func(tk *ticket.Ticket) {
				tk.SLAPausedSeconds = 50000
				tk.PendingSince = ref(now.Add(-time.Hour))
			},
			record: `first_response_at = $2::timestamptz, response_elapsed_at_met_seconds = 10, resolved_at = $3::timestamptz, resolution_elapsed_at_met_seconds = 72000`,
			args: func(tk ticket.Ticket) []any {
				return []any{tk.CreatedAt.Add(10 * time.Second), tk.CreatedAt.Add(72000 * time.Second)}
			}},
		// No frozen number (a record migration 000028 could only estimate):
		// StatusFor recomputes at the met instant, and so does the SQL.
		{name: "met with no frozen reading, 90% at the met instant", age: resp(500), record: `first_response_at = $2::timestamptz, resolved_at = $2::timestamptz`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(resp(90))} }},
		{name: "met with no frozen reading, 110% at the met instant", age: resp(500), record: `first_response_at = $2::timestamptz, resolved_at = $2::timestamptz`,
			args: func(tk ticket.Ticket) []any { return []any{tk.CreatedAt.Add(resp(110))} }},

		{name: "no SLA record", age: resp(500), noRecord: true},
	}

	type made struct {
		name   string
		id     uuid.UUID
		bucket string
	}
	var rows []made
	for i, sc := range scenarios {
		created := now.Add(-sc.age)
		tk := f.mk(spec{subject: sc.name, created: created, updated: created, seq: int64(500 + i)})
		if sc.mutate != nil {
			sc.mutate(&tk)
			require.NoError(t, f.ts.Update(f.ctx, tk))
		}
		if !sc.noRecord {
			require.NoError(t, f.sls.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: pol.ID}))
			if sc.record != "" {
				args := []any{tk.ID}
				if sc.args != nil {
					args = append(args, sc.args(tk)...)
				}
				_, err := f.tx.ExecContext(f.ctx, `UPDATE sla_records SET `+sc.record+` WHERE ticket_id = $1`, args...)
				require.NoError(t, err, sc.name)
			}
		}
		rows = append(rows, made{name: sc.name, id: tk.ID})
	}

	// A superseded record is history: it is red, and the current record is
	// not, so reading it would put the ticket in the wrong bucket.
	hist := f.mk(spec{subject: "history ignored", created: now.Add(-resp(10)), seq: 900})
	_, err := f.tx.ExecContext(f.ctx, `INSERT INTO sla_records (ticket_id, policy_id, superseded_at, started_at) VALUES ($1, $2, $3, $4)`,
		hist.ID, pol.ID, now.Add(-time.Hour), now.Add(-resp(500)))
	require.NoError(t, err)
	require.NoError(t, f.sls.CreateRecord(f.ctx, sla.Record{TicketID: hist.ID, PolicyID: pol.ID}))
	rows = append(rows, made{name: "history ignored", id: hist.ID})

	// The oracle: StatusFor on what the store reads back.
	byID := map[uuid.UUID]*made{}
	for i := range rows {
		m := &rows[i]
		byID[m.id] = m
		tk, err := f.ts.GetByID(f.ctx, m.id)
		require.NoError(t, err)
		rec, err := f.sls.GetRecord(f.ctx, m.id)
		if errors.Is(err, sla.ErrNoRecord) {
			m.bucket = "other" // not under an SLA
			continue
		}
		require.NoError(t, err)
		st := sla.StatusFor(rec, pol, tk, now)
		switch {
		case st.Response.Color == sla.Red || st.Resolution.Color == sla.Red:
			m.bucket = "breached"
		case st.Response.Color == sla.Amber || st.Resolution.Color == sla.Amber:
			m.bucket = "at_risk"
		default:
			m.bucket = "other"
		}
	}
	count := map[string]int{}
	for _, m := range rows {
		count[m.bucket]++
	}
	require.Positive(t, count["breached"], "the scenarios must exercise every bucket")
	require.Positive(t, count["at_risk"])
	require.Positive(t, count["other"])

	fl := f.all()
	fl.Limit = 500
	inBucket := func(state ticket.SLAState) map[uuid.UUID]bool {
		x := fl
		x.SLA = state
		got, err := f.ts.ListFilteredAt(f.ctx, x, now)
		require.NoError(t, err)
		out := map[uuid.UUID]bool{}
		for _, tk := range got {
			out[tk.ID] = true
		}
		return out
	}
	breached, atRisk := inBucket(ticket.SLABreached), inBucket(ticket.SLAAtRisk)
	for _, m := range rows {
		require.Equal(t, m.bucket == "breached", breached[m.id], "%q: oracle says %s, breached filter says %v", m.name, m.bucket, breached[m.id])
		require.Equal(t, m.bucket == "at_risk", atRisk[m.id], "%q: oracle says %s, at_risk filter says %v", m.name, m.bucket, atRisk[m.id])
	}

	// The sort uses the same buckets: breached, then at risk, then the rest,
	// and the reverse, each ending created_at DESC, id.
	rank := map[string]int{"breached": 0, "at_risk": 1, "other": 2}
	for _, order := range []ticket.SortOrder{ticket.OrderAsc, ticket.OrderDesc} {
		x := fl
		x.Sort, x.Order = ticket.SortSLA, order
		got, err := f.ts.ListFilteredAt(f.ctx, x, now)
		require.NoError(t, err)
		prev := -1
		if order == ticket.OrderDesc {
			prev = 3
		}
		for _, tk := range got {
			m, ok := byID[tk.ID]
			if !ok {
				continue // the six fixture tickets carry no record
			}
			r := rank[m.bucket]
			if order == ticket.OrderAsc {
				require.GreaterOrEqual(t, r, prev, "sla asc: %q out of order", m.name)
			} else {
				require.LessOrEqual(t, r, prev, "sla desc: %q out of order", m.name)
			}
			prev = r
		}
	}
}

// 000060 adds indexes only. Down drops them, up brings them back, and neither
// touches a row.
func TestMigration060_TicketFilterIndexes(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	indexes := []string{"tickets_updated_at_idx", "tickets_guest_name_trgm_idx", "tickets_guest_email_trgm_idx"}
	have := func(name string) bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'tickets' AND indexname = $1)`, name).Scan(&ok))
		return ok
	}
	for _, name := range indexes {
		require.True(t, have(name), "%s exists once the migrations have run", name)
	}
	execMigrationFile(t, ctx, tx, "migrations/000060_ticket_filter_indexes.down.sql", nil)
	for _, name := range indexes {
		require.False(t, have(name), "down drops %s", name)
	}
	execMigrationFile(t, ctx, tx, "migrations/000060_ticket_filter_indexes.up.sql", nil)
	execMigrationFile(t, ctx, tx, "migrations/000060_ticket_filter_indexes.up.sql", nil) // IF NOT EXISTS: running twice is harmless
	for _, name := range indexes {
		require.True(t, have(name), "up restores %s", name)
	}
}

// 000061 adds the index the scoped list uses for a group that covers one type
// of a category. Index only, and partial: most tickets have no type.
func TestMigration061_TicketTypeIndex(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	const name = "tickets_type_id_idx"
	have := func() bool {
		var ok bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'tickets' AND indexname = $1)`, name).Scan(&ok))
		return ok
	}
	require.True(t, have(), "exists once the migrations have run")
	execMigrationFile(t, ctx, tx, "migrations/000061_ticket_type_index.down.sql", nil)
	require.False(t, have(), "down drops it")
	execMigrationFile(t, ctx, tx, "migrations/000061_ticket_type_index.up.sql", nil)
	execMigrationFile(t, ctx, tx, "migrations/000061_ticket_type_index.up.sql", nil) // IF NOT EXISTS: running twice is harmless
	require.True(t, have(), "up restores it")
}
