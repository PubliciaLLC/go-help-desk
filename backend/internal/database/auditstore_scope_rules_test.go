package database_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The staff scope's Category/Type rule is written in SQL twice (the page query
// and the count, which states it as two hashable IN lists so it is evaluated
// once instead of once per ticket). What the rule means is small and easy to
// get subtly wrong:
//
//   - a rule with no type covers the whole category, tickets with no type
//     included;
//   - a rule with a type covers that type only, and NOT a ticket whose type is
//     NULL, nor another type of the same category;
//   - several routes to one ticket (two groups, a category rule and a type rule,
//     being the reporter as well) show its entries once, not once per route.
//
// This test states those three in Go, over a matrix built to hit each, and holds
// both statements to it. Expected sets come from scopeCovers below, never from
// the query.

type scopeRule struct {
	cat uuid.UUID
	typ *uuid.UUID
}

type scopeTicket struct {
	t   ticket.Ticket
	ids []uuid.UUID // the audit entries planted on it
}

// scopeCovers is the rule, restated: ticket.CanView's Category/Type branch.
func scopeCovers(rules []scopeRule, t ticket.Ticket) bool {
	for _, r := range rules {
		if r.cat != t.CategoryID {
			continue
		}
		if r.typ == nil {
			return true
		}
		if t.TypeID != nil && *t.TypeID == *r.typ {
			return true
		}
	}
	return false
}

func TestAuditStore_Search_ScopeRuleShapes(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	us, cs, gs, ts := userstore.New(q), categorystore.New(q), groupstore.New(q), ticketstore.New(q)
	aus := auditstore.New(q)

	mkUser := func(name string, role user.Role) user.User {
		u := user.User{ID: uuid.New(), Email: name + "@rules.local", DisplayName: name, Role: role,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	reporter := mkUser("rules-reporter", user.RoleUser)

	mkCat := func(name string, order int) category.Category {
		c := category.Category{ID: uuid.New(), Name: name, SortOrder: order, Active: true}
		require.NoError(t, cs.CreateCategory(ctx, c))
		return c
	}
	mkType := func(c category.Category, name string) uuid.UUID {
		tp := category.Type{ID: uuid.New(), CategoryID: c.ID, Name: name, SortOrder: 1, Active: true}
		require.NoError(t, cs.CreateType(ctx, tp))
		return tp.ID
	}
	catX, catY, catZ := mkCat("RulesX", 1), mkCat("RulesY", 2), mkCat("RulesZ", 3)
	x1, x2 := mkType(catX, "X1"), mkType(catX, "X2")
	y1, y2 := mkType(catY, "Y1"), mkType(catY, "Y2")

	// Every (category, type) shape a rule can meet, reported by someone who is
	// none of the staff below, so only a rule or a relationship can admit them.
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	seq := int64(5000)
	mkTicket := func(cat uuid.UUID, typ *uuid.UUID, mod func(*ticket.Ticket)) ticket.Ticket {
		seq++
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
			Subject: "rules", CategoryID: cat, TypeID: typ, Priority: ticket.PriorityLow, StatusID: newSt.ID,
			ReporterUserID: &reporter.ID, CreatedAt: now, UpdatedAt: now,
		}
		if mod != nil {
			mod(&tk)
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}
	ptr := func(u uuid.UUID) *uuid.UUID { return &u }

	mkGroup := func(name string, members []uuid.UUID, rules ...scopeRule) uuid.UUID {
		g := group.Group{ID: uuid.New(), Name: name}
		require.NoError(t, gs.Create(ctx, g))
		for _, m := range members {
			require.NoError(t, gs.AddMember(ctx, g.ID, m))
		}
		for _, r := range rules {
			require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: r.cat, TypeID: r.typ}))
		}
		return g.ID
	}

	// Staff, one per shape of rule set.
	overlap := mkUser("rules-overlap", user.RoleStaff)
	typeOnly := mkUser("rules-type-only", user.RoleStaff)
	catAndType := mkUser("rules-cat-and-type", user.RoleStaff)
	mixed := mkUser("rules-mixed", user.RoleStaff)
	twoTypes := mkUser("rules-two-types", user.RoleStaff)
	noRule := mkUser("rules-no-rule", user.RoleStaff)
	viaGroup := mkUser("rules-via-group", user.RoleStaff)

	rulesOf := map[uuid.UUID][]scopeRule{}
	groupsOf := map[uuid.UUID][]uuid.UUID{}
	grant := func(u user.User, name string, rules ...scopeRule) uuid.UUID {
		rulesOf[u.ID] = append(rulesOf[u.ID], rules...)
		g := mkGroup(name, []uuid.UUID{u.ID}, rules...)
		groupsOf[u.ID] = append(groupsOf[u.ID], g)
		return g
	}
	// overlap: two groups with the same category rule, and a type rule inside it.
	gOverlapA := grant(overlap, "ov-a", scopeRule{catX.ID, nil})
	grant(overlap, "ov-b", scopeRule{catX.ID, nil}, scopeRule{catX.ID, &x1})
	grant(typeOnly, "type-only", scopeRule{catY.ID, &y1})
	grant(catAndType, "cat-and-type", scopeRule{catY.ID, nil}, scopeRule{catY.ID, &y1})
	grant(mixed, "mixed", scopeRule{catX.ID, nil}, scopeRule{catY.ID, &y1})
	grant(twoTypes, "two-types-a", scopeRule{catY.ID, &y1})
	grant(twoTypes, "two-types-b", scopeRule{catY.ID, &y2})
	grant(noRule, "no-rule") // a group, and no rule
	gVia := grant(viaGroup, "via-group")

	var tickets []scopeTicket
	for _, c := range []struct {
		cat uuid.UUID
		typ *uuid.UUID
	}{
		{catX.ID, nil}, {catX.ID, &x1}, {catX.ID, &x2},
		{catY.ID, nil}, {catY.ID, &y1}, {catY.ID, &y2},
		{catZ.ID, nil},
	} {
		tickets = append(tickets, scopeTicket{t: mkTicket(c.cat, c.typ, nil)})
	}
	// A ticket in a category nobody covers, assigned to a group with no rule:
	// the group is the only way in.
	tickets = append(tickets, scopeTicket{t: mkTicket(catZ.ID, nil, func(tk *ticket.Ticket) { tk.AssigneeGroupID = ptr(gVia) })})
	// Every other way in to one ticket in catX, on top of the rules that already
	// cover it: reported by, assigned to, and assigned to a group of, the same
	// staff member. It must still show each entry once.
	tickets = append(tickets, scopeTicket{t: mkTicket(catX.ID, &x1, func(tk *ticket.Ticket) {
		tk.ReporterUserID = &overlap.ID
		tk.AssigneeUserID = &overlap.ID
		tk.AssigneeGroupID = ptr(gOverlapA)
	})})

	const action = "rule_shapes"
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	n := 0
	plant := func(entityType string, entityID uuid.UUID) uuid.UUID {
		n++
		id := uuid.New()
		require.NoError(t, q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
			ID: id, EntityType: entityType, EntityID: entityID, Action: action, CreatedAt: at.Add(time.Duration(n) * time.Second),
		}))
		return id
	}
	for i := range tickets {
		tickets[i].ids = []uuid.UUID{plant("ticket", tickets[i].t.ID), plant("ticket", tickets[i].t.ID)}
	}
	// Neither of these is a ticket entry on a ticket, whoever asks.
	plant("ticket", uuid.New())
	plant("user", tickets[0].t.ID)

	inGroup := func(u user.User, g *uuid.UUID) bool {
		if g == nil {
			return false
		}
		for _, mine := range groupsOf[u.ID] {
			if mine == *g {
				return true
			}
		}
		return false
	}
	related := func(u user.User, tk ticket.Ticket) bool {
		return (tk.ReporterUserID != nil && *tk.ReporterUserID == u.ID) ||
			(tk.AssigneeUserID != nil && *tk.AssigneeUserID == u.ID) ||
			inGroup(u, tk.AssigneeGroupID)
	}

	for _, s := range []struct {
		name string
		u    user.User
	}{{"overlapping routes", overlap}, {"type rule only", typeOnly}, {"category and type rule", catAndType},
		{"category rule and a type rule elsewhere", mixed}, {"two groups with one type rule each", twoTypes}, {"a group with no rule", noRule}, {"assigned to a group, no rule", viaGroup}} {
		t.Run(s.name, func(t *testing.T) {
			want := []uuid.UUID{}
			for _, tk := range tickets {
				if related(s.u, tk.t) || scopeCovers(rulesOf[s.u.ID], tk.t) {
					want = append(want, tk.ids...)
				}
			}
			sortIDs(want)

			fl := audit.Filter{Action: action, ScopedTo: &s.u.ID}
			all, err := aus.Search(ctx, fl, 500, 0)
			require.NoError(t, err)
			got := make([]uuid.UUID, len(all.Entries))
			for i, e := range all.Entries {
				got[i] = e.ID
			}
			sortIDs(got)
			require.Equal(t, want, got, "the page is not the set the rule admits (a repeat here means a ticket was reached by more than one route)")
			require.Equal(t, len(want), all.Total, "the count disagrees with the page: a ticket is counted per route, or a rule is read differently")
			require.False(t, all.TotalCapped)

			// The same answer one entry at a time: total is stable and exact on every page.
			for off := 0; off < len(want); off++ {
				pg, err := aus.Search(ctx, fl, 1, off)
				require.NoError(t, err)
				require.Len(t, pg.Entries, 1)
				require.Equal(t, len(want), pg.Total, "offset %d", off)
				require.Equal(t, off+1 < len(want), pg.HasMore, "offset %d", off)
			}
		})
	}

	// The matrix is not vacuous: the shapes differ from one another, and the
	// NULL-type ticket is what separates a category rule from a type rule.
	t.Run("the fixture separates the rules", func(t *testing.T) {
		count := func(u user.User) int {
			pg, err := aus.Search(ctx, audit.Filter{Action: action, ScopedTo: &u.ID}, 500, 0)
			require.NoError(t, err)
			return pg.Total
		}
		require.Equal(t, 2, count(typeOnly), "one type of one category, entries on nothing else")
		require.Equal(t, 6, count(catAndType), "all of catY: the typeless ticket and both types, two entries each")
		require.Equal(t, 4, count(twoTypes), "both types of catY but not the typeless ticket")
		require.Equal(t, 0, count(noRule))
		require.Equal(t, 2, count(viaGroup), "the one ticket assigned to their group, nothing else")
	})
}

func sortIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
}
