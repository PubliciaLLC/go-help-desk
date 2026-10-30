package database_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TestAuditStore_TicketScopeLookup verifies that GetAuditTicketScope reads
// the staff member's group membership and rules correctly, returning the four
// arrays that SearchAuditLogScoped and CountAuditLogScoped need: the groups
// they belong to, the categories they cover whole (no type), and the categories
// and types they cover partially (with a type). Mutations on any of these
// arrays or on the lookup's SQL filter fail this test.
func TestAuditStore_TicketScopeLookup(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	ctx := context.Background()
	us, gs, cs := userstore.New(q), groupstore.New(q), categorystore.New(q)

	mkUser := func(name string) user.User {
		u := user.User{
			ID:          uuid.New(),
			Email:       name + "@lookup.local",
			DisplayName: name,
			Role:        user.RoleStaff,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		require.NoError(t, us.Create(ctx, u))
		return u
	}

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

	// Build the fixture for the test cases.
	cat1 := mkCat("Cat1", 1)
	cat2 := mkCat("Cat2", 2)
	typ2_cat1 := mkType(cat1, "Typ2Cat1")
	typ1_cat2 := mkType(cat2, "Typ1Cat2")

	noGroup := mkUser("no-group")
	groupNoRule := mkUser("group-no-rule")
	catRuleOnly := mkUser("cat-rule-only")
	typeRuleOnly := mkUser("type-rule-only")
	twoGroupsCatAndType := mkUser("two-groups-cat-and-type")
	userNotInRule := mkUser("user-not-in-rule")

	// Create groups and assign scope rules.
	gEmpty := group.Group{ID: uuid.New(), Name: "empty"}
	require.NoError(t, gs.Create(ctx, gEmpty))
	require.NoError(t, gs.AddMember(ctx, gEmpty.ID, groupNoRule.ID))

	gCat1 := group.Group{ID: uuid.New(), Name: "cat1-rule"}
	require.NoError(t, gs.Create(ctx, gCat1))
	require.NoError(t, gs.AddMember(ctx, gCat1.ID, catRuleOnly.ID))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: gCat1.ID, CategoryID: cat1.ID, TypeID: nil}))

	gTyp := group.Group{ID: uuid.New(), Name: "type-rule"}
	require.NoError(t, gs.Create(ctx, gTyp))
	require.NoError(t, gs.AddMember(ctx, gTyp.ID, typeRuleOnly.ID))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: gTyp.ID, CategoryID: cat2.ID, TypeID: &typ1_cat2}))

	gCat2 := group.Group{ID: uuid.New(), Name: "cat2-rule"}
	require.NoError(t, gs.Create(ctx, gCat2))
	require.NoError(t, gs.AddMember(ctx, gCat2.ID, twoGroupsCatAndType.ID))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: gCat2.ID, CategoryID: cat1.ID, TypeID: nil}))

	gType2 := group.Group{ID: uuid.New(), Name: "type2-rule"}
	require.NoError(t, gs.Create(ctx, gType2))
	require.NoError(t, gs.AddMember(ctx, gType2.ID, twoGroupsCatAndType.ID))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: gType2.ID, CategoryID: cat2.ID, TypeID: &typ1_cat2}))

	// A group and rule the user is not in.
	gOther := group.Group{ID: uuid.New(), Name: "other-rule"}
	require.NoError(t, gs.Create(ctx, gOther))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: gOther.ID, CategoryID: cat1.ID, TypeID: &typ2_cat1}))

	// Helper to assert arrays are equal as sorted sets (order doesn't matter).
	idsEqual := func(got, want []uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		gotCopy := make([]uuid.UUID, len(got))
		wantCopy := make([]uuid.UUID, len(want))
		copy(gotCopy, got)
		copy(wantCopy, want)
		sort.Slice(gotCopy, func(i, j int) bool { return gotCopy[i].String() < gotCopy[j].String() })
		sort.Slice(wantCopy, func(i, j int) bool { return wantCopy[i].String() < wantCopy[j].String() })
		for i := range gotCopy {
			if gotCopy[i] != wantCopy[i] {
				return false
			}
		}
		return true
	}

	// Helper to get the scope for a user.
	getScope := func(userID uuid.UUID) dbgen.GetAuditTicketScopeRow {
		scope, err := q.GetAuditTicketScope(ctx, userID)
		require.NoError(t, err)
		return scope
	}

	cases := []struct {
		name            string
		user            user.User
		wantGroupIDs    []uuid.UUID
		wantCategoryIDs []uuid.UUID
		wantTypedCatIDs []uuid.UUID
		wantTypeIDs     []uuid.UUID
	}{
		{
			name:            "user in no group",
			user:            noGroup,
			wantGroupIDs:    []uuid.UUID{},
			wantCategoryIDs: []uuid.UUID{},
			wantTypedCatIDs: []uuid.UUID{},
			wantTypeIDs:     []uuid.UUID{},
		},
		{
			name:            "group with no rule",
			user:            groupNoRule,
			wantGroupIDs:    []uuid.UUID{gEmpty.ID},
			wantCategoryIDs: []uuid.UUID{},
			wantTypedCatIDs: []uuid.UUID{},
			wantTypeIDs:     []uuid.UUID{},
		},
		{
			name:            "category rule only",
			user:            catRuleOnly,
			wantGroupIDs:    []uuid.UUID{gCat1.ID},
			wantCategoryIDs: []uuid.UUID{cat1.ID},
			wantTypedCatIDs: []uuid.UUID{},
			wantTypeIDs:     []uuid.UUID{},
		},
		{
			name:            "type rule only",
			user:            typeRuleOnly,
			wantGroupIDs:    []uuid.UUID{gTyp.ID},
			wantCategoryIDs: []uuid.UUID{},
			wantTypedCatIDs: []uuid.UUID{cat2.ID},
			wantTypeIDs:     []uuid.UUID{typ1_cat2},
		},
		{
			name:            "two groups: one category, one type rule",
			user:            twoGroupsCatAndType,
			wantGroupIDs:    []uuid.UUID{gCat2.ID, gType2.ID},
			wantCategoryIDs: []uuid.UUID{cat1.ID},
			wantTypedCatIDs: []uuid.UUID{cat2.ID},
			wantTypeIDs:     []uuid.UUID{typ1_cat2},
		},
		{
			name:            "rule on a group the user is not in: not visible",
			user:            userNotInRule,
			wantGroupIDs:    []uuid.UUID{},
			wantCategoryIDs: []uuid.UUID{},
			wantTypedCatIDs: []uuid.UUID{},
			wantTypeIDs:     []uuid.UUID{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope := getScope(tc.user.ID)

			// Compare as sorted sets: nil and empty slice both represent zero items.
			require.True(t, idsEqual(scope.GroupIds, tc.wantGroupIDs), "group_ids mismatch")
			require.True(t, idsEqual(scope.CategoryIds, tc.wantCategoryIDs), "category_ids mismatch")
			require.True(t, idsEqual(scope.TypedCategoryIds, tc.wantTypedCatIDs), "typed_category_ids mismatch")
			require.True(t, idsEqual(scope.TypeIds, tc.wantTypeIDs), "type_ids mismatch")
		})
	}
}
