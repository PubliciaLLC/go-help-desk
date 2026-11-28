package userstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// A new account follows the device (#709): CreateUser does not name the column,
// so the default is what every account starts with.
func TestTheme_DefaultsToDevice(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)

	id := seedStaff(t, store)
	got, err := store.GetByID(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDevice, got.Theme)
}

// SetTheme writes the one column: not the name, and not updated_at, because the
// account did not change.
func TestTheme_SetWritesOnlyTheTheme(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	id := seedStaff(t, store)
	before, err := store.GetByID(ctx, id)
	require.NoError(t, err)

	for _, th := range []user.Theme{user.ThemeDark, user.ThemeLight, user.ThemeDevice} {
		require.NoError(t, store.SetTheme(ctx, id, th))
		got, err := store.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, th, got.Theme)
		require.True(t, before.UpdatedAt.Equal(got.UpdatedAt), "updated_at moved: the account did not change")
		require.Equal(t, before.DisplayName, got.DisplayName)
		require.Equal(t, before.Role, got.Role)
	}
}

// The database refuses a value the service would have: the CHECK is the backstop.
func TestTheme_DatabaseRefusesAnUnknownValue(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)

	id := seedStaff(t, store)
	require.Error(t, store.SetTheme(context.Background(), id, user.Theme("auto")))
}

// A deleted account, or one that never existed, is not found rather than
// reported as saved.
func TestTheme_SetOnAMissingAccountIsNotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	id := seedStaff(t, store)
	require.NoError(t, store.SoftDelete(ctx, id))
	err := store.SetTheme(ctx, id, user.ThemeDark)
	require.True(t, errors.Is(err, user.ErrNotFound), "got %v", err)

	err = store.SetTheme(ctx, uuid.New(), user.ThemeDark)
	require.True(t, errors.Is(err, user.ErrNotFound), "got %v", err)
}

// The whole-row Update used by the admin edit does not carry the theme, so
// saving a user page cannot put back an older choice.
func TestTheme_AWholeRowUpdateLeavesItAlone(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	store := userstore.New(q)
	ctx := context.Background()

	id := seedStaff(t, store)
	require.NoError(t, store.SetTheme(ctx, id, user.ThemeDark))

	u, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	u.DisplayName = "Renamed"
	u.Theme = user.ThemeLight // a stale copy, as a read-modify-write would carry
	u.UpdatedAt = time.Now().UTC()
	require.NoError(t, store.Update(ctx, u))

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Renamed", got.DisplayName)
	require.Equal(t, user.ThemeDark, got.Theme, "an unrelated save undid the person's choice")
}
