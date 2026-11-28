package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #626(c): a field is attached at one level of a path. The store refuses the
// second attachment, in the transaction that holds the definition's row, and
// leaves overlaps that already exist alone.

type overlapTree struct {
	cat, otherCat uuid.UUID
	catName       string
	typ, otherTyp uuid.UUID // two types of cat
	item          uuid.UUID // under typ
	foreignTyp    uuid.UUID // a type of otherCat
}

func newOverlapTree(t *testing.T, cs *categorystore.Store) overlapTree {
	t.Helper()
	ctx := context.Background()
	mkCat := func(name string) (uuid.UUID, string) {
		c := category.Category{ID: uuid.New(), Name: name + " " + uuid.NewString()[:8], SortOrder: 1, Active: true}
		require.NoError(t, cs.CreateCategory(ctx, c))
		return c.ID, c.Name
	}
	mkType := func(cat uuid.UUID, name string) uuid.UUID {
		ty := category.Type{ID: uuid.New(), CategoryID: cat, Name: name, SortOrder: 1, Active: true}
		require.NoError(t, cs.CreateType(ctx, ty))
		return ty.ID
	}
	var tr overlapTree
	tr.cat, tr.catName = mkCat("Hardware")
	tr.otherCat, _ = mkCat("Software")
	tr.typ, tr.otherTyp, tr.foreignTyp = mkType(tr.cat, "Laptop"), mkType(tr.cat, "Phone"), mkType(tr.otherCat, "IDE")
	it := category.Item{ID: uuid.New(), TypeID: tr.typ, Name: "Dell", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateItem(ctx, it))
	tr.item = it.ID
	return tr
}

func TestCreateAssignment_RefusesAFieldAlreadyAboveOrBelow(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	cs, fs := categorystore.New(q), customfieldstore.New(q, nil)
	tr := newOverlapTree(t, cs)

	newField := func() uuid.UUID {
		d, err := fs.CreateFieldDef(ctx, customfield.FieldDef{
			ID: uuid.New(), Name: "Overlap-" + uuid.NewString()[:8], FieldType: customfield.FieldTypeText, Active: true,
		}, nil)
		require.NoError(t, err)
		return d.ID
	}
	attach := func(field uuid.UUID, scope customfield.ScopeType, id uuid.UUID) error {
		_, err := fs.CreateAssignment(ctx, customfield.Assignment{ID: uuid.New(), FieldDefID: field, ScopeType: scope, ScopeID: id}, nil)
		return err
	}
	cat, typ, item := customfield.CategoryScope, customfield.TypeScope, customfield.ItemScope

	for _, tc := range []struct {
		name   string
		first  func() (customfield.ScopeType, uuid.UUID)
		second func() (customfield.ScopeType, uuid.UUID)
		where  string // the first attachment's path, which the refusal names
	}{
		{"type under a category it is on",
			func() (customfield.ScopeType, uuid.UUID) { return cat, tr.cat },
			func() (customfield.ScopeType, uuid.UUID) { return typ, tr.typ }, ""},
		{"item under a category it is on",
			func() (customfield.ScopeType, uuid.UUID) { return cat, tr.cat },
			func() (customfield.ScopeType, uuid.UUID) { return item, tr.item }, ""},
		{"item under a type it is on",
			func() (customfield.ScopeType, uuid.UUID) { return typ, tr.typ },
			func() (customfield.ScopeType, uuid.UUID) { return item, tr.item }, " › Laptop"},
		{"category above a type it is on",
			func() (customfield.ScopeType, uuid.UUID) { return typ, tr.typ },
			func() (customfield.ScopeType, uuid.UUID) { return cat, tr.cat }, " › Laptop"},
		{"category above an item it is on",
			func() (customfield.ScopeType, uuid.UUID) { return item, tr.item },
			func() (customfield.ScopeType, uuid.UUID) { return cat, tr.cat }, " › Laptop › Dell"},
		{"type above an item it is on",
			func() (customfield.ScopeType, uuid.UUID) { return item, tr.item },
			func() (customfield.ScopeType, uuid.UUID) { return typ, tr.typ }, " › Laptop › Dell"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newField()
			st, id := tc.first()
			require.NoError(t, attach(f, st, id))
			st, id = tc.second()
			err := attach(f, st, id)
			require.ErrorIs(t, err, customfield.ErrConflict)
			require.Contains(t, err.Error(), tr.catName+tc.where, "the refusal says where the field already is")
			places, err := fs.ListPlacements(ctx, f)
			require.NoError(t, err)
			require.Len(t, places, 1, "the refused attachment was not written")
		})
	}

	t.Run("places that share no path are fine", func(t *testing.T) {
		f := newField()
		require.NoError(t, attach(f, typ, tr.typ))
		require.NoError(t, attach(f, typ, tr.otherTyp), "a sibling type")
		require.NoError(t, attach(f, cat, tr.otherCat), "another category")
		// And its types: the other category's own field is not the same path.
		require.ErrorIs(t, attach(f, typ, tr.foreignTyp), customfield.ErrConflict, "a type under the category just attached")
	})

	t.Run("the same place twice is still the old conflict", func(t *testing.T) {
		f := newField()
		require.NoError(t, attach(f, cat, tr.cat))
		err := attach(f, cat, tr.cat)
		require.ErrorIs(t, err, customfield.ErrConflict)
		require.Contains(t, err.Error(), "already assigned here")
	})

	t.Run("a field that does not exist is a bad reference, not a 500", func(t *testing.T) {
		require.ErrorIs(t, attach(uuid.New(), cat, tr.cat), customfield.ErrInvalidReference)
	})

	t.Run("an overlap that already exists is left alone", func(t *testing.T) {
		f := newField()
		// Seeded under the store, as a 1.3.0-beta database would hold it.
		for _, p := range []struct {
			st customfield.ScopeType
			id uuid.UUID
		}{{cat, tr.cat}, {typ, tr.typ}} {
			_, err := q.CreateCustomFieldAssignment(ctx, dbgen.CreateCustomFieldAssignmentParams{
				ID: uuid.New(), FieldDefID: f, ScopeType: string(p.st), ScopeID: p.id,
			})
			require.NoError(t, err)
		}
		as, err := fs.ListAssignments(ctx, customfield.TypeScope, tr.typ)
		require.NoError(t, err)
		var onType customfield.Assignment
		for _, a := range as {
			if a.FieldDefID == f {
				onType = a
			}
		}
		require.NotEqual(t, uuid.Nil, onType.ID)

		onType.RequiredOnNew = true
		require.NoError(t, fs.UpdateAssignment(ctx, onType, func(customfield.Assignment, customfield.AssignmentInfo) []audit.Entry { return nil }),
			"changing an existing overlapping attachment is not an attach")
		require.NoError(t, fs.DeleteAssignment(ctx, onType.ID, func(customfield.Assignment, customfield.AssignmentInfo) audit.Entry { return audit.Entry{} }) /* removes the overlap */)
	})
}

// The new fields travel through the store.
func TestAssignments_CarryGuestFlagAndRetiredOptions(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	cs, fs := categorystore.New(q), customfieldstore.New(q, nil)
	tr := newOverlapTree(t, cs)

	def, err := fs.CreateFieldDef(ctx, customfield.FieldDef{
		ID: uuid.New(), Name: "Carry-" + uuid.NewString()[:8], FieldType: customfield.FieldTypeSelect,
		Options: []string{"a"}, RetiredOptions: []string{"b"}, Active: true,
	}, nil)
	require.NoError(t, err)
	made, err := fs.CreateAssignment(ctx, customfield.Assignment{
		ID: uuid.New(), FieldDefID: def.ID, ScopeType: customfield.CategoryScope, ScopeID: tr.cat,
		VisibleOnNew: true, VisibleToGuests: true,
	}, nil)
	require.NoError(t, err)
	require.True(t, made.VisibleToGuests)

	got, err := fs.ListAssignments(ctx, customfield.CategoryScope, tr.cat)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].VisibleToGuests)
	require.Equal(t, []string{"b"}, got[0].FieldDef.RetiredOptions, "ticket routes return retired_options")

	got[0].VisibleToGuests = false
	require.NoError(t, fs.UpdateAssignment(ctx, got[0], func(customfield.Assignment, customfield.AssignmentInfo) []audit.Entry { return nil }))
	again, err := fs.GetAssignment(ctx, got[0].ID)
	require.NoError(t, err)
	require.False(t, again.VisibleToGuests)

	places, err := fs.ListPlacements(ctx, def.ID)
	require.NoError(t, err)
	require.Len(t, places, 1)
}
