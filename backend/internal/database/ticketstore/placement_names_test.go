package ticketstore_test

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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// PlacementNames reads the names a created-ticket chat message shows, from the
// ids the event holds, in one statement. Against real Postgres: a level that
// is not set, or whose row is gone, comes back "" and is never an error.
func TestPlacementNames(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	ts := ticketstore.New(q)

	suffix := uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Millisecond)
	cat := category.Category{ID: uuid.New(), Name: "Hardware " + suffix, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	typ := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: `Printer "&" <b> ` + suffix, Active: true}
	require.NoError(t, categorystore.New(q).CreateType(ctx, typ))
	item := category.Item{ID: uuid.New(), TypeID: typ.ID, Name: "Tóner ☕ " + suffix, Active: true}
	require.NoError(t, categorystore.New(q).CreateItem(ctx, item))
	grp := group.Group{ID: uuid.New(), Name: "IT Support " + suffix}
	require.NoError(t, groupstore.New(q).Create(ctx, grp))
	staff := user.User{ID: uuid.New(), Email: "placement-" + suffix + "@test.local", DisplayName: "Jordan Blake " + suffix,
		Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, userstore.New(q).Create(ctx, staff))
	gone := user.User{ID: uuid.New(), Email: "placement-gone-" + suffix + "@test.local", DisplayName: "Former Person",
		Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, userstore.New(q).Create(ctx, gone))
	require.NoError(t, userstore.New(q).SoftDelete(ctx, gone.ID))

	cases := []struct {
		name string
		st   notification.TicketState
		want notification.Placement
	}{
		{"category only", notification.TicketState{CategoryID: cat.ID},
			notification.Placement{Category: cat.Name}},
		{"category and type", notification.TicketState{CategoryID: cat.ID, TypeID: &typ.ID},
			notification.Placement{Category: cat.Name, Type: typ.Name}},
		{"category, type and item", notification.TicketState{CategoryID: cat.ID, TypeID: &typ.ID, ItemID: &item.ID},
			notification.Placement{Category: cat.Name, Type: typ.Name, Item: item.Name}},
		{"group only", notification.TicketState{CategoryID: cat.ID, AssigneeGroupID: &grp.ID},
			notification.Placement{Category: cat.Name, Group: grp.Name}},
		{"assignee only", notification.TicketState{CategoryID: cat.ID, AssigneeUserID: &staff.ID},
			notification.Placement{Category: cat.Name, Assignee: staff.DisplayName}},
		{"everything", notification.TicketState{CategoryID: cat.ID, TypeID: &typ.ID, ItemID: &item.ID, AssigneeGroupID: &grp.ID, AssigneeUserID: &staff.ID},
			notification.Placement{Category: cat.Name, Type: typ.Name, Item: item.Name, Group: grp.Name, Assignee: staff.DisplayName}},
		// A name that is gone is "", not an error and not a stale name: the
		// message leaves the level out.
		{"ids that no longer exist", notification.TicketState{CategoryID: uuid.New(), TypeID: ptr(uuid.New()), ItemID: ptr(uuid.New()),
			AssigneeGroupID: ptr(uuid.New()), AssigneeUserID: ptr(uuid.New())}, notification.Placement{}},
		{"a deleted account has no name to show", notification.TicketState{CategoryID: cat.ID, AssigneeUserID: &gone.ID},
			notification.Placement{Category: cat.Name}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ts.PlacementNames(ctx, tc.st)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
