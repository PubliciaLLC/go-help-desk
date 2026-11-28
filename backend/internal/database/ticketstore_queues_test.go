package database_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The queues of #676 (assigned_to_me, my_groups, unassigned) and the held_by
// filter, held to the classification table in DESIGN.md "Ticket list views"
// over a real Postgres. Each ticket's subject is "Q <group>-<holder>" so the
// table can be read off the names. The fixture's own six tickets are left in
// place and are ignored by qset, because they are not part of the table.

type queueFixture struct {
	*searchFixture
	v, o, w    user.User // v is the viewer, o is someone else, w is in no group
	g1, g2, g3 group.Group
}

func newQueueFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := &queueFixture{searchFixture: newSearchFixture(t)}
	gs := groupstore.New(dbgen.New(f.tx))
	mkStaff := func(name string) user.User {
		u := user.User{ID: uuid.New(), Email: name + "@queues.example", DisplayName: name, Role: user.RoleStaff,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, f.us.Create(f.ctx, u))
		return u
	}
	f.v, f.o, f.w = mkStaff("viewer"), mkStaff("other"), mkStaff("loner")
	mkGroup := func(name string) group.Group {
		g := group.Group{ID: uuid.New(), Name: name}
		require.NoError(t, gs.Create(f.ctx, g))
		return g
	}
	f.g1, f.g2, f.g3 = mkGroup("QueueG1"), mkGroup("QueueG2"), mkGroup("QueueG3")
	require.NoError(t, gs.AddMember(f.ctx, f.g1.ID, f.v.ID))
	require.NoError(t, gs.AddMember(f.ctx, f.g2.ID, f.v.ID))
	require.NoError(t, gs.AddMember(f.ctx, f.g3.ID, f.o.ID)) // v is not in G3

	// What v's groups cover, for the scoped tests: all of catB (G1) and one
	// type of catA (G2).
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: f.g1.ID, CategoryID: f.catB.ID}))
	require.NoError(t, gs.AddScope(f.ctx, group.GroupScope{GroupID: f.g2.ID, CategoryID: f.catA.ID, TypeID: &f.typeA.ID}))

	// Every row of the classification table, for a viewer in G1 and G2, plus a
	// second group of theirs and a group they are not in.
	f.mk(spec{subject: "Q none-nobody"})
	f.mk(spec{subject: "Q none-V", assignee: &f.v, desc: "zebra"})
	f.mk(spec{subject: "Q none-O", assignee: &f.o})
	f.mk(spec{subject: "Q G1-nobody", group: &f.g1, status: &f.stResolved})
	f.mk(spec{subject: "Q G1-V", group: &f.g1, assignee: &f.v, status: &f.stResolved, desc: "zebra"})
	f.mk(spec{subject: "Q G1-O", group: &f.g1, assignee: &f.o, desc: "zebra"})
	f.mk(spec{subject: "Q G1-disabled", group: &f.g1, assignee: &f.disabled}) // a disabled account is someone else
	f.mk(spec{subject: "Q G2-nobody", group: &f.g2})
	f.mk(spec{subject: "Q G2-O", group: &f.g2, assignee: &f.o})
	f.mk(spec{subject: "Q G3-nobody", group: &f.g3})
	f.mk(spec{subject: "Q G3-V", group: &f.g3, assignee: &f.v})
	f.mk(spec{subject: "Q G3-O", group: &f.g3, assignee: &f.o, desc: "zebra"})
	return f
}

// qset is the sorted subjects of the table's tickets that fl returns.
func (f *queueFixture) qset(fl ticket.Filter) []string {
	f.t.Helper()
	out := []string{}
	for _, s := range f.list(fl) {
		if strings.HasPrefix(s, "Q ") {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func (f *queueFixture) as(u user.User, vis ticket.Visibility, q ticket.Queue) ticket.Filter {
	return ticket.Filter{ActorID: u.ID, Visibility: vis, Queue: q, Limit: 200}
}

var (
	qAssignedToMe = []string{"Q G1-V", "Q G3-V", "Q none-V"}
	qMyGroups     = []string{"Q G1-O", "Q G1-V", "Q G1-disabled", "Q G1-nobody", "Q G2-O", "Q G2-nobody"}
	qAvailable    = []string{"Q none-nobody"}
)

func TestListFiltered_QueueAssignedToMeAndMyGroups(t *testing.T) {
	f := newQueueFixture(t)

	require.Equal(t, qAssignedToMe, f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueAssignedToMe)))
	require.Equal(t, qMyGroups, f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups)))
	require.Equal(t, qAvailable, f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueUnassigned)))

	t.Run("assigned_to_me and my_groups together are mine", func(t *testing.T) {
		for _, u := range []user.User{f.v, f.o, f.w, f.staff} {
			// Whole results, fixture tickets included: the identity holds for
			// every viewer, not only for the table's.
			mine := f.set(f.as(u, ticket.VisibilityAll, ticket.QueueMine))
			union := append(f.list(f.as(u, ticket.VisibilityAll, ticket.QueueAssignedToMe)),
				f.list(f.as(u, ticket.VisibilityAll, ticket.QueueMyGroups))...)
			slices.Sort(union)
			require.Equal(t, mine, slices.Compact(union), u.DisplayName)
		}
		require.Equal(t, []string{"Q G1-O", "Q G1-V", "Q G1-disabled", "Q G1-nobody", "Q G2-O", "Q G2-nobody", "Q G3-V", "Q none-V"},
			f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueMine)))
	})

	t.Run("a ticket in both a person's and their group's queue is listed once", func(t *testing.T) {
		got := f.list(f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups))
		require.Len(t, slices.Compact(slices.Sorted(slices.Values(got))), len(got))
	})

	t.Run("a viewer in no group has an empty My Group's Tickets", func(t *testing.T) {
		require.Empty(t, f.list(f.as(f.w, ticket.VisibilityAll, ticket.QueueMyGroups)))
		require.Empty(t, f.list(f.as(f.w, ticket.VisibilityScoped, ticket.QueueMyGroups)))
		require.Empty(t, f.list(f.as(f.w, ticket.VisibilityAll, ticket.QueueAssignedToMe)))
	})

	t.Run("a group the viewer left no longer counts", func(t *testing.T) {
		require.NoError(t, groupstore.New(dbgen.New(f.tx)).RemoveMember(f.ctx, f.g2.ID, f.v.ID))
		require.Equal(t, []string{"Q G1-O", "Q G1-V", "Q G1-disabled", "Q G1-nobody"}, f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups)))
	})
}

func TestListFiltered_HeldBy(t *testing.T) {
	f := newQueueFixture(t)
	myGroups := f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups)
	held := func(fl ticket.Filter, h ticket.HeldBy) []string {
		fl.HeldBy = h
		return f.qset(fl)
	}

	nobody := held(myGroups, ticket.HeldNobody)
	me := held(myGroups, ticket.HeldMe)
	others := held(myGroups, ticket.HeldOthers)
	require.Equal(t, []string{"Q G1-nobody", "Q G2-nobody"}, nobody)
	require.Equal(t, []string{"Q G1-V"}, me)
	require.Equal(t, []string{"Q G1-O", "Q G1-disabled", "Q G2-O"}, others)

	t.Run("the three partition My Group's Tickets", func(t *testing.T) {
		all := slices.Concat(nobody, me, others)
		slices.Sort(all)
		require.Equal(t, qMyGroups, all, "every ticket in exactly one of the three")
		require.Equal(t, qMyGroups, held(myGroups, ""), "absent is no restriction")
	})

	t.Run("on every queue", func(t *testing.T) {
		any := f.as(f.v, ticket.VisibilityAll, ticket.QueueAny)
		require.Equal(t, []string{"Q G1-nobody", "Q G2-nobody", "Q G3-nobody", "Q none-nobody"}, held(any, ticket.HeldNobody))
		require.Equal(t, qAssignedToMe, held(any, ticket.HeldMe))
		require.Equal(t, []string{"Q G1-O", "Q G1-disabled", "Q G2-O", "Q G3-O", "Q none-O"}, held(any, ticket.HeldOthers))

		atm := f.as(f.v, ticket.VisibilityAll, ticket.QueueAssignedToMe)
		require.Equal(t, qAssignedToMe, held(atm, ticket.HeldMe))
		require.Empty(t, held(atm, ticket.HeldNobody), "a ticket assigned to me has an assignee user")
		require.Empty(t, held(atm, ticket.HeldOthers))

		avail := f.as(f.v, ticket.VisibilityAll, ticket.QueueUnassigned)
		require.Equal(t, qAvailable, held(avail, ticket.HeldNobody))
		require.Empty(t, held(avail, ticket.HeldMe))
	})

	t.Run("relative to the caller", func(t *testing.T) {
		// The same tickets, asked as the other account: o holds G1-O, G2-O,
		// G3-O and none-O, so those are "me" and v's are "others".
		any := f.as(f.o, ticket.VisibilityAll, ticket.QueueAny)
		require.Equal(t, []string{"Q G1-O", "Q G2-O", "Q G3-O", "Q none-O"}, held(any, ticket.HeldMe))
		require.Contains(t, held(any, ticket.HeldOthers), "Q G1-V")
		require.NotContains(t, held(any, ticket.HeldOthers), "Q G1-O")
	})
}

// A queue is ANDed with visibility, never widened by it, and scoped staff get
// the Available queue narrowed to what their groups cover.
func TestListFiltered_QueuesUnderScope(t *testing.T) {
	f := newQueueFixture(t)

	t.Run("assigned_to_me and my_groups equal the unscoped result", func(t *testing.T) {
		for _, q := range []ticket.Queue{ticket.QueueAssignedToMe, ticket.QueueMyGroups} {
			for _, u := range []user.User{f.v, f.o, f.w} {
				require.Equal(t,
					f.set(f.as(u, ticket.VisibilityAll, q)),
					f.set(f.as(u, ticket.VisibilityScoped, q)),
					"queue %d, %s", q, u.DisplayName)
			}
		}
	})

	t.Run("held_by does not change that", func(t *testing.T) {
		for _, h := range []ticket.HeldBy{ticket.HeldNobody, ticket.HeldMe, ticket.HeldOthers} {
			scoped, all := f.as(f.v, ticket.VisibilityScoped, ticket.QueueMyGroups), f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups)
			scoped.HeldBy, all.HeldBy = h, h
			require.Equal(t, f.set(all), f.set(scoped), "held_by=%s", h)
		}
	})

	t.Run("unassigned is only what the viewer's scope admits", func(t *testing.T) {
		// None of these is assigned to a group or a person.
		f.mk(spec{subject: "Q avail catA", cat: &f.catA})                                         // not covered
		f.mk(spec{subject: "Q avail catB", cat: &f.catB})                                         // G1 covers all of catB
		f.mk(spec{subject: "Q avail typeA", cat: &f.catA, typ: &f.typeA})                         // G2 covers this type
		f.mk(spec{subject: "Q avail typeB", cat: &f.catA, typ: &f.typeB})                         // same category, other type
		f.mk(spec{subject: "Q avail reported by v", cat: &f.catA, typ: &f.typeB, reporter: &f.v}) // not covered, but theirs

		unscoped := f.qset(f.as(f.v, ticket.VisibilityAll, ticket.QueueUnassigned))
		require.Equal(t, []string{"Q avail catA", "Q avail catB", "Q avail reported by v", "Q avail typeA", "Q avail typeB", "Q none-nobody"}, unscoped)

		require.Equal(t, []string{"Q avail catB", "Q avail reported by v", "Q avail typeA"},
			f.qset(f.as(f.v, ticket.VisibilityScoped, ticket.QueueUnassigned)))
		// Not only the table's tickets: the fixture's own unassigned ones follow
		// the same rule (charlie and Delta are in catB).
		require.Equal(t, []string{"Delta", "Q avail catB", "Q avail reported by v", "Q avail typeA", "charlie"},
			f.set(f.as(f.v, ticket.VisibilityScoped, ticket.QueueUnassigned)))

		// Staff in no group see only what they reported.
		require.Empty(t, f.list(f.as(f.w, ticket.VisibilityScoped, ticket.QueueUnassigned)))
		f.mk(spec{subject: "Q avail reported by w", cat: &f.catA, reporter: &f.w})
		require.Equal(t, []string{"Q avail reported by w"}, f.list(f.as(f.w, ticket.VisibilityScoped, ticket.QueueUnassigned)))
	})
}

// The queues are ANDed with every other filter, including the ones that used
// to replace a queue: a group picked inside My Group's Tickets narrows it.
func TestListFiltered_QueuesCombine(t *testing.T) {
	f := newQueueFixture(t)
	mg := f.as(f.v, ticket.VisibilityAll, ticket.QueueMyGroups)
	atm := f.as(f.v, ticket.VisibilityAll, ticket.QueueAssignedToMe)

	t.Run("text search narrows within the queue", func(t *testing.T) {
		q := mg
		q.Query = "zebra"
		require.Equal(t, []string{"Q G1-O", "Q G1-V"}, f.qset(q), "G3-O and none-V match the text but are not in the queue")
		q = atm
		q.Query = "zebra"
		require.Equal(t, []string{"Q G1-V", "Q none-V"}, f.qset(q))
		q.HeldBy = ticket.HeldNobody
		require.Empty(t, f.qset(q))
		q = mg
		q.Query, q.HeldBy = "zebra", ticket.HeldOthers
		require.Equal(t, []string{"Q G1-O"}, f.qset(q))
	})

	t.Run("status", func(t *testing.T) {
		q := mg
		q.StatusIDs = []uuid.UUID{f.stResolved.ID}
		require.Equal(t, []string{"Q G1-V", "Q G1-nobody"}, f.qset(q))
		q.HeldBy = ticket.HeldMe
		require.Equal(t, []string{"Q G1-V"}, f.qset(q))
		q = atm
		q.StatusIDs = []uuid.UUID{f.stResolved.ID}
		require.Equal(t, []string{"Q G1-V"}, f.qset(q))
	})

	t.Run("assignee_group_id narrows and never widens", func(t *testing.T) {
		q := mg
		q.AssigneeGroupID = &f.g2.ID
		require.Equal(t, []string{"Q G2-O", "Q G2-nobody"}, f.qset(q))
		q.HeldBy = ticket.HeldNobody
		require.Equal(t, []string{"Q G2-nobody"}, f.qset(q))
		q = mg
		q.AssigneeGroupID = &f.g3.ID
		require.Empty(t, f.qset(q), "G3 is not one of the viewer's groups: the queue still applies")
		q = atm
		q.AssigneeGroupID = &f.g3.ID
		require.Equal(t, []string{"Q G3-V"}, f.qset(q))
	})

	t.Run("an assignee person narrows", func(t *testing.T) {
		q := mg
		q.AssigneeUserID = &f.o.ID
		require.Equal(t, []string{"Q G1-O", "Q G2-O"}, f.qset(q))
		q.HeldBy = ticket.HeldMe
		require.Empty(t, f.qset(q), "both apply")
	})
}
