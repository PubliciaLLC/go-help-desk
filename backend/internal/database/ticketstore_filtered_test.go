package database_test

import (
	"context"
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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// ListFiltered carries both the optional filters and the visibility rule in one
// statement, so it is only really tested against Postgres — a fake store would
// re-implement the predicate being verified and prove nothing.

type filteredFixture struct {
	ts *ticketstore.Store

	reporter, staff, other user.User
	catA, catB             category.Category
	newSt, resolvedSt      ticket.Status

	// reporterTicket is reported by reporter and assigned to nobody.
	// assignedTicket is reported by other and assigned to staff.
	// scopedTicket   is reported by other, unassigned, in catB which staff's
	//                group holds a scope over.
	// strangerTicket is reported by other and touches staff in no way at all.
	reporterTicket, assignedTicket, scopedTicket, strangerTicket ticket.Ticket
}

func newFilteredFixture(t *testing.T, q *dbgen.Queries) *filteredFixture {
	t.Helper()
	ctx := context.Background()
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	gs := groupstore.New(q)

	mkUser := func(email string, role user.Role) user.User {
		u := user.User{
			ID: uuid.New(), Email: email, DisplayName: email, Role: role,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	f := &filteredFixture{ts: ts}
	f.reporter = mkUser("filt-reporter@example.com", user.RoleUser)
	f.staff = mkUser("filt-staff@example.com", user.RoleStaff)
	f.other = mkUser("filt-other@example.com", user.RoleUser)

	f.catA = category.Category{ID: uuid.New(), Name: "FiltA", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, f.catA))
	f.catB = category.Category{ID: uuid.New(), Name: "FiltB", SortOrder: 2, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, f.catB))

	var err error
	f.newSt, err = ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	f.resolvedSt, err = ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)

	// staff's group holds a category-level scope over catB.
	g := group.Group{ID: uuid.New(), Name: "FiltGroup"}
	require.NoError(t, gs.Create(ctx, g))
	require.NoError(t, gs.AddMember(ctx, g.ID, f.staff.ID))
	// nil TypeID: a category-level scope covering every type beneath catB.
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: f.catB.ID}))

	seq := int64(100)
	mkTicket := func(subject string, cat uuid.UUID, reporter *uuid.UUID, assignee *uuid.UUID, st uuid.UUID, p ticket.Priority) ticket.Ticket {
		seq++
		now := time.Now().UTC().Truncate(time.Millisecond)
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
			Subject:        subject,
			Description:    subject + " description",
			CategoryID:     cat,
			Priority:       p,
			StatusID:       st,
			ReporterUserID: reporter,
			AssigneeUserID: assignee,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		return tk
	}

	f.reporterTicket = mkTicket("Filt reporter ticket", f.catA.ID, &f.reporter.ID, nil, f.newSt.ID, ticket.PriorityHigh)
	f.assignedTicket = mkTicket("Filt assigned ticket", f.catA.ID, &f.other.ID, &f.staff.ID, f.newSt.ID, ticket.PriorityLow)
	f.scopedTicket = mkTicket("Filt scoped ticket", f.catB.ID, &f.other.ID, nil, f.resolvedSt.ID, ticket.PriorityMedium)
	f.strangerTicket = mkTicket("Filt stranger ticket", f.catA.ID, &f.other.ID, nil, f.newSt.ID, ticket.PriorityMedium)
	return f
}

func subjects(ts []ticket.Ticket) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		out[t.Subject] = true
	}
	return out
}

func TestListFiltered_Visibility(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q)

	t.Run("reporter sees only what they reported", func(t *testing.T) {
		got, err := f.ts.ListFiltered(ctx, ticket.Filter{
			ActorID: f.reporter.ID, Visibility: ticket.VisibilityReporter, Limit: 50,
		})
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.reporterTicket.Subject])
		require.False(t, s[f.assignedTicket.Subject], "a reporting user must not see someone else's ticket")
		require.False(t, s[f.scopedTicket.Subject])
		require.False(t, s[f.strangerTicket.Subject])
	})

	t.Run("scoped staff see assigned and in-scope tickets", func(t *testing.T) {
		got, err := f.ts.ListFiltered(ctx, ticket.Filter{
			ActorID: f.staff.ID, Visibility: ticket.VisibilityScoped, Limit: 50,
		})
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.assignedTicket.Subject], "assigned to them")
		require.True(t, s[f.scopedTicket.Subject], "in a category their group scopes")
		require.False(t, s[f.strangerTicket.Subject], "out of scope and unassigned")
		require.False(t, s[f.reporterTicket.Subject], "someone else's, out of scope")
	})

	t.Run("unrestricted sees everything", func(t *testing.T) {
		got, err := f.ts.ListFiltered(ctx, ticket.Filter{
			ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50,
		})
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.reporterTicket.Subject])
		require.True(t, s[f.assignedTicket.Subject])
		require.True(t, s[f.scopedTicket.Subject])
		require.True(t, s[f.strangerTicket.Subject])
	})
}

func TestListFiltered_Filters(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q)
	base := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50}

	t.Run("by status", func(t *testing.T) {
		fl := base
		fl.StatusID = &f.resolvedSt.ID
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.scopedTicket.Subject])
		require.False(t, s[f.strangerTicket.Subject], "a New ticket must not match a Resolved filter")
	})

	t.Run("by priority", func(t *testing.T) {
		fl := base
		p := ticket.PriorityHigh
		fl.Priority = &p
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.reporterTicket.Subject])
		require.False(t, s[f.assignedTicket.Subject])
	})

	t.Run("by category", func(t *testing.T) {
		fl := base
		fl.CategoryID = &f.catB.ID
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.scopedTicket.Subject])
		require.False(t, s[f.reporterTicket.Subject])
	})

	t.Run("by assignee", func(t *testing.T) {
		fl := base
		fl.AssigneeUserID = &f.staff.ID
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.assignedTicket.Subject])
		require.False(t, s[f.reporterTicket.Subject], "an unassigned ticket must not match an assignee filter")
	})

	t.Run("by full-text query", func(t *testing.T) {
		fl := base
		fl.Query = "scoped"
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.scopedTicket.Subject])
		require.False(t, s[f.strangerTicket.Subject])
	})

	t.Run("by tracking number", func(t *testing.T) {
		fl := base
		fl.Query = string(f.scopedTicket.TrackingNumber)
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		require.True(t, subjects(got)[f.scopedTicket.Subject])
	})

	// Filters compose: two filters narrow, they do not widen.
	t.Run("filters combine", func(t *testing.T) {
		fl := base
		fl.CategoryID = &f.catA.ID
		p := ticket.PriorityHigh
		fl.Priority = &p
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.reporterTicket.Subject])
		require.False(t, s[f.assignedTicket.Subject], "matches category but not priority")
		require.False(t, s[f.scopedTicket.Subject], "matches neither")
	})

	// A visibility mode must still bound a filtered query — filtering is not a
	// way around the scope rule.
	t.Run("filters do not escape visibility", func(t *testing.T) {
		fl := ticket.Filter{
			ActorID: f.reporter.ID, Visibility: ticket.VisibilityReporter, Limit: 50,
			CategoryID: &f.catA.ID,
		}
		got, err := f.ts.ListFiltered(ctx, fl)
		require.NoError(t, err)
		s := subjects(got)
		require.True(t, s[f.reporterTicket.Subject])
		require.False(t, s[f.strangerTicket.Subject],
			"a category filter must not reveal a ticket the actor cannot see")
	})
}

func TestListFiltered_Paginates(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q)
	base := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 2}

	first, err := f.ts.ListFiltered(ctx, base)
	require.NoError(t, err)
	require.Len(t, first, 2)

	second := base
	second.Offset = 2
	rest, err := f.ts.ListFiltered(ctx, second)
	require.NoError(t, err)
	require.NotEmpty(t, rest)

	// The two pages must not overlap.
	for _, a := range first {
		for _, b := range rest {
			require.NotEqual(t, a.ID, b.ID, "offset must advance the window, not repeat it")
		}
	}
}

// TestGetByID_NotFoundWording pins the exact text a missing ticket produces.
//
// internal/mcp reproduces this string when it refuses a ticket the caller may
// not see, so that "does not exist" and "not yours" are indistinguishable. If
// this wording changes, mcp.notFoundFor must change with it — otherwise
// get_ticket becomes an oracle for which tracking numbers are real. The paired
// assertion is mcp.TestNotFoundFor_MatchesStoreWording.
func TestGetByID_NotFoundWording(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	missing := uuid.New()
	_, err := ticketstore.New(q).GetByID(context.Background(), missing)
	require.Error(t, err)
	require.Equal(t, "not found: ticket "+missing.String(), err.Error())
}
