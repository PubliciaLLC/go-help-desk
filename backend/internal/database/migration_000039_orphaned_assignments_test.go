package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Migration 000039 removes custom-field assignments whose category, type or
// item no longer exists, and leaves every other one alone (#419).
func TestMigration039_DeletesOnlyOrphanedFieldAssignments(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	cs, fs := categorystore.New(q), customfieldstore.New(q)

	cat := category.Category{ID: uuid.New(), Name: "M39 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	tp := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "T", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(ctx, tp))
	it := category.Item{ID: uuid.New(), TypeID: tp.ID, Name: "I", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateItem(ctx, it))
	def, err := fs.CreateFieldDef(ctx, customfield.FieldDef{
		ID: uuid.New(), Name: "M39-" + uuid.NewString()[:8], FieldType: customfield.FieldTypeText, Active: true,
	})
	require.NoError(t, err)

	mk := func(scope customfield.ScopeType, id uuid.UUID) uuid.UUID {
		d := def
		if scope != customfield.CategoryScope { // one assignment per (field, scope, id), so any field will do
			d, err = fs.CreateFieldDef(ctx, customfield.FieldDef{
				ID: uuid.New(), Name: "M39-" + uuid.NewString()[:8], FieldType: customfield.FieldTypeText, Active: true,
			})
			require.NoError(t, err)
		}
		a, err := fs.CreateAssignment(ctx, customfield.Assignment{
			ID: uuid.New(), FieldDefID: d.ID, ScopeType: scope, ScopeID: id,
		})
		require.NoError(t, err)
		return a.ID
	}
	live := []uuid.UUID{mk(customfield.CategoryScope, cat.ID), mk(customfield.TypeScope, tp.ID), mk(customfield.ItemScope, it.ID)}
	orphans := []uuid.UUID{
		mk(customfield.CategoryScope, uuid.New()), mk(customfield.TypeScope, uuid.New()), mk(customfield.ItemScope, uuid.New()),
	}

	execMigrationFile(t, ctx, tx, "migrations/000039_drop_orphaned_field_assignments.up.sql", nil)

	exists := func(id uuid.UUID) bool {
		var n int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM custom_field_assignments WHERE id = $1`, id).Scan(&n))
		return n == 1
	}
	for _, id := range live {
		require.True(t, exists(id), "a live assignment was deleted")
	}
	for _, id := range orphans {
		require.False(t, exists(id), "an orphaned assignment was left")
	}
}
