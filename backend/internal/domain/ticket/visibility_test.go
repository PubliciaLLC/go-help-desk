package ticket_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// DESIGN.md describes staff visibility twice and not identically — "tickets
// within their scope" (roles table) and "assigned to them and their groups"
// (search). CanView implements the union, so both readings hold.
//
// The cases that matter are the boundaries: a ticket in your category that
// nobody has picked up (visible, or the queue stalls), and one outside every
// scope you hold (not visible, or the rule means nothing).

func ptr(u uuid.UUID) *uuid.UUID { return &u }

func TestCanView_Admin(t *testing.T) {
	admin := uuid.New()
	other := uuid.New()
	tkt := ticket.Ticket{ID: uuid.New(), CategoryID: uuid.New(), ReporterUserID: &other}

	require.True(t, ticket.CanView(tkt, ticket.Actor{UserID: &admin, Role: user.RoleAdmin}, ticket.StaffScope{}),
		"an admin sees everything, with no groups and no scopes")
}

func TestCanView_ReportingUser(t *testing.T) {
	me, someoneElse := uuid.New(), uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleUser}

	mine := ticket.Ticket{ID: uuid.New(), CategoryID: uuid.New(), ReporterUserID: &me}
	theirs := ticket.Ticket{ID: uuid.New(), CategoryID: uuid.New(), ReporterUserID: &someoneElse}

	require.True(t, ticket.CanView(mine, actor, ticket.StaffScope{}))
	require.False(t, ticket.CanView(theirs, actor, ticket.StaffScope{}),
		"a reporting user never sees someone else's ticket, scope or no scope")

	// Group membership must not widen a reporting user's view.
	wide := ticket.StaffScope{
		GroupIDs: []uuid.UUID{uuid.New()},
		Scopes:   []ticket.ScopeRule{{CategoryID: theirs.CategoryID}},
	}
	require.False(t, ticket.CanView(theirs, actor, wide),
		"scope applies to staff; it must not promote a reporting user")
}

func TestCanView_Staff(t *testing.T) {
	me := uuid.New()
	myGroup, otherGroup := uuid.New(), uuid.New()
	myCategory, otherCategory := uuid.New(), uuid.New()
	myType, otherType := uuid.New(), uuid.New()

	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}
	scope := ticket.StaffScope{
		GroupIDs: []uuid.UUID{myGroup},
		Scopes:   []ticket.ScopeRule{{CategoryID: myCategory, TypeID: &myType}},
	}

	cases := []struct {
		name string
		tkt  ticket.Ticket
		want bool
	}{
		{
			name: "in my category and type, unassigned",
			tkt:  ticket.Ticket{CategoryID: myCategory, TypeID: &myType},
			want: true, // the queue case — nobody has picked it up yet
		},
		{
			name: "in my category but a type I do not hold",
			tkt:  ticket.Ticket{CategoryID: myCategory, TypeID: &otherType},
			want: false,
		},
		{
			name: "outside every scope I hold",
			tkt:  ticket.Ticket{CategoryID: otherCategory, TypeID: &otherType},
			want: false,
		},
		{
			name: "outside my scope but assigned to me",
			tkt:  ticket.Ticket{CategoryID: otherCategory, TypeID: &otherType, AssigneeUserID: &me},
			want: true, // the other half of the union
		},
		{
			name: "outside my scope but assigned to my group",
			tkt:  ticket.Ticket{CategoryID: otherCategory, AssigneeGroupID: &myGroup},
			want: true,
		},
		{
			name: "assigned to a group I am not in",
			tkt:  ticket.Ticket{CategoryID: otherCategory, AssigneeGroupID: &otherGroup},
			want: false,
		},
		{
			name: "outside my scope but I reported it",
			tkt:  ticket.Ticket{CategoryID: otherCategory, ReporterUserID: &me},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ticket.CanView(tc.tkt, actor, scope))
		})
	}
}

// TestCanView_CategoryLevelScope pins the rule from DESIGN.md that a
// category-level scope (nil type) covers every type beneath it, and that items
// never factor in.
func TestCanView_CategoryLevelScope(t *testing.T) {
	me := uuid.New()
	cat, typeA, itemA := uuid.New(), uuid.New(), uuid.New()

	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}
	scope := ticket.StaffScope{Scopes: []ticket.ScopeRule{{CategoryID: cat, TypeID: nil}}}

	require.True(t, ticket.CanView(ticket.Ticket{CategoryID: cat, TypeID: &typeA}, actor, scope),
		"a category-level scope covers every type beneath it")
	require.True(t, ticket.CanView(ticket.Ticket{CategoryID: cat}, actor, scope),
		"including a ticket with no type set")
	require.True(t, ticket.CanView(ticket.Ticket{CategoryID: cat, TypeID: &typeA, ItemID: &itemA}, actor, scope),
		"items do not factor into scope")
}

// TestCanView_StaffWithNoScope is the state an instance is in the moment
// enforcement is switched on before any group is configured. It must be closed,
// not open — an empty scope list meaning "everything" is the kind of default
// that turns a setting into a no-op.
func TestCanView_StaffWithNoScope(t *testing.T) {
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}

	require.False(t, ticket.CanView(ticket.Ticket{CategoryID: uuid.New()}, actor, ticket.StaffScope{}),
		"no groups and no scopes must mean no access, not full access")

	// Their own work stays reachable, so they are not locked out entirely.
	require.True(t, ticket.CanView(ticket.Ticket{CategoryID: uuid.New(), AssigneeUserID: &me}, actor, ticket.StaffScope{}))
}

func TestCanView_NilActor(t *testing.T) {
	require.False(t, ticket.CanView(ticket.Ticket{}, ticket.Actor{Role: user.RoleStaff}, ticket.StaffScope{}),
		"a staff actor with no user ID cannot be matched against anything")
}
