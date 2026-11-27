package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// A setting with no row is "unset", and the domain has to be able to tell that
// from a read that failed (admin.SiteNames logs the second and not the first).
func TestAdminStoreGet_UnsetIsNotFound(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, rollback := testutil.TxQueries(t, db)
	t.Cleanup(rollback)
	store := adminstore.New(q)
	ctx := context.Background()

	_, err := store.Get(ctx, "no_such_setting")
	require.ErrorIs(t, err, admin.ErrSettingNotFound)
	require.ErrorIs(t, err, sql.ErrNoRows, "the cause is kept")

	require.NoError(t, store.Set(ctx, "a_setting", []byte(`"x"`)))
	raw, err := store.Get(ctx, "a_setting")
	require.NoError(t, err)
	require.JSONEq(t, `"x"`, string(raw))

	// A fault is not "not found".
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = store.Get(cancelled, "a_setting")
	require.Error(t, err)
	require.False(t, errors.Is(err, admin.ErrSettingNotFound))
}
