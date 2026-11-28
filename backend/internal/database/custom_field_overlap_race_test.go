package database_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Two attachments of one field, one at a category and one at its type, made at
// the same moment, are checked one after the other: each looks for the other's,
// so only one lands. Without the lock on the definition both would look, find
// nothing, and write.
//
// Staged rather than raced: an administrator change holds the definition's row,
// both attaches start and must wait for it (a race would pass with or without
// the lock), then the change lets go.
func TestCreateAssignment_ConcurrentAttachesAtTwoLevelsCannotBothLand(t *testing.T) {
	f := newLockFixture(t)
	ctx := context.Background()
	cs := categorystore.New(f.db.Queries)
	cat := category.Category{ID: uuid.New(), Name: "Overlap race", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	typ := category.Type{ID: uuid.New(), CategoryID: cat.ID, Name: "Laptop", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateType(ctx, typ))
	def := f.field("Asset tag", customfield.FieldTypeText)

	held := f.holdChange(def.ID, nil, func(*customfield.FieldDef) {})

	attach := func(scope customfield.ScopeType, id uuid.UUID) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := f.svc.Attach(ctx, customfield.Assignment{FieldDefID: def.ID, ScopeType: scope, ScopeID: id}, nil)
			done <- err
		}()
		return done
	}
	atCategory, atType := attach(customfield.CategoryScope, cat.ID), attach(customfield.TypeScope, typ.ID)
	require.True(t, stillWaiting(atCategory), "an attach waits for the definition's lock")
	require.True(t, stillWaiting(atType), "and so does the other")

	held.proceed()
	require.NoError(t, within(t, held.done, "the held change"))
	errs := []error{within(t, atCategory, "the attach at the category"), within(t, atType, "the attach at the type")}

	landed := 0
	for _, err := range errs {
		if err == nil {
			landed++
			continue
		}
		require.ErrorIs(t, err, customfield.ErrConflict)
	}
	require.Equal(t, 1, landed, "one of the two attachments landed and the other was refused")

	places, err := f.svc.ListPlacements(ctx, def.ID)
	require.NoError(t, err)
	require.Len(t, places, 1)
}
