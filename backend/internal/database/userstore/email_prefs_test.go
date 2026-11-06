package userstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The three staff-email switches are on for a new account (#441), which is also
// what migration 000042's DEFAULT TRUE did to every existing row.
func TestEmailPreferences_DefaultToOn(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)

	id := seedStaff(t, store)
	got, err := store.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.True(t, got.EmailNotifyAssigned)
	require.True(t, got.EmailNotifyActivity)
	require.True(t, got.EmailNotifyGroup)
}

// A patch changes the switches it names and nothing else: not the other
// switches, not the role or the name.
func TestEmailPreferences_PatchTouchesOnlyWhatItNames(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	id := seedStaff(t, store)
	before, err := store.GetByID(ctx, id)
	require.NoError(t, err)

	off, on := false, true
	require.NoError(t, store.SetEmailPreferences(ctx, id, user.EmailPreferencesPatch{Activity: &off}))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EmailNotifyAssigned)
	require.False(t, got.EmailNotifyActivity)
	require.True(t, got.EmailNotifyGroup)
	require.Equal(t, before.Role, got.Role)
	require.Equal(t, before.DisplayName, got.DisplayName)
	require.Equal(t, before.PasswordHash, got.PasswordHash)

	// Several at once, and back on again.
	require.NoError(t, store.SetEmailPreferences(ctx, id, user.EmailPreferencesPatch{Assigned: &off, Group: &off, Activity: &on}))
	got, err = store.GetByID(ctx, id)
	require.NoError(t, err)
	require.False(t, got.EmailNotifyAssigned)
	require.True(t, got.EmailNotifyActivity)
	require.False(t, got.EmailNotifyGroup)

	// An empty patch is a no-op, not an error.
	require.NoError(t, store.SetEmailPreferences(ctx, id, user.EmailPreferencesPatch{}))
	again, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, got.EmailNotifyAssigned, again.EmailNotifyAssigned)
	require.Equal(t, got.EmailNotifyActivity, again.EmailNotifyActivity)
	require.Equal(t, got.EmailNotifyGroup, again.EmailNotifyGroup)
}

// The whole-row Update used by the admin edit does not carry the switches, so
// saving an unrelated change cannot turn a person's choice back on.
func TestEmailPreferences_AWholeRowUpdateLeavesThemAlone(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	id := seedStaff(t, store)
	off := false
	require.NoError(t, store.SetEmailPreferences(ctx, id, user.EmailPreferencesPatch{Assigned: &off}))

	u, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	u.DisplayName = "Renamed"
	u.EmailNotifyAssigned = true // a stale copy, as a read-modify-write would carry
	require.NoError(t, store.Update(ctx, u))

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.False(t, got.EmailNotifyAssigned, "an unrelated save undid the person's choice")
}
