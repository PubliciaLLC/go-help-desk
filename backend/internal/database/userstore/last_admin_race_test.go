package userstore_test

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// An instance cannot be left with no administrator, however the requests
// arrive.
//
// The first version of this guard counted the other administrators and then
// wrote — two statements with nothing between them, and two requests that
// both counted before either wrote both passed. Measured against a live
// database: two administrators demoting each other left zero administrators
// in twenty-eight rounds out of thirty. It did not need two people either —
// one administrator sending "remove Bob" and "remove me" together did it in
// all thirty.
//
// Run against a real connection pool rather than the usual rolled-back
// transaction, because the race is between concurrent statements and a single
// transaction serialises them by construction — which is exactly why the
// defect survived the tests written for it.
// Two of these cannot both decide they are allowed, because the second waits
// for the first and then re-reads.
//
// The first version of this guard counted the other administrators in Go and
// then wrote — two statements with nothing between them, and two requests
// that both counted before either wrote both passed. Measured against a live
// server: two administrators demoting each other left zero administrators in
// twenty-eight rounds out of thirty, and one administrator sending "remove
// Bob" and "remove me" together did it in all thirty. Setup does not reopen,
// so that is an instance nobody can administer again.
//
// The property is tested by holding the two statements open at once in
// separate transactions, rather than by racing goroutines and hoping they
// overlap. A race that reproduces one time in ten is a test that passes nine
// times against a broken guard; this either serialises or it does not.
func TestLastAdminGuard_SerialisesTwoRemovals(t *testing.T) {
	cases := []struct {
		name string
		stmt string
	}{
		{"disable", "DisableUserUnlessLastAdmin"},
		{"delete", "SoftDeleteUserUnlessLastAdmin"},
		{"demote", "SetUserRoleUnlessLastAdmin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, closeDB := freshDatabase(t)
			defer closeDB()
			store := userstore.New(dbgen.New(db.SQL))
			ctx := context.Background()

			a := seedAdmin(t, store)
			b := seedAdmin(t, store)
			require.Equal(t, 2, activeAdmins(t, db))

			// The real store methods, each bound to its own transaction, so
			// this exercises exactly the statements that run in production.
			run := func(tx *sql.Tx, id uuid.UUID) (bool, error) {
				st := userstore.New(dbgen.New(tx))
				switch tc.stmt {
				case "DisableUserUnlessLastAdmin":
					return st.DisableUnlessLastAdmin(ctx, id)
				case "SoftDeleteUserUnlessLastAdmin":
					return st.SoftDeleteUnlessLastAdmin(ctx, id)
				default:
					return st.SetRoleUnlessLastAdmin(ctx, id, "staff")
				}
			}

			// The first removal, held open.
			tx1, err := db.SQL.BeginTx(ctx, nil)
			require.NoError(t, err)
			// Rolled back whatever happens, including when an assertion
			// below ends the test early: a transaction left open holds locks
			// that stop the throwaway database being dropped, and the
			// failure then looks like a hang instead of a failure.
			t.Cleanup(func() { _ = tx1.Rollback() })
			applied1, err := run(tx1, b)
			require.NoError(t, err)
			require.True(t, applied1, "the first removal should be allowed: two administrators exist")

			// The second, started while the first is still uncommitted. It
			// must WAIT — which is the whole point. If it answers straight
			// away, both are deciding on the same stale picture.
			second := make(chan bool, 1)
			go func() {
				tx2, err := db.SQL.BeginTx(ctx, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = tx2.Rollback() }()
				applied, err := run(tx2, a)
				if err != nil {
					t.Error(err)
					return
				}
				if applied {
					_ = tx2.Commit()
				}
				second <- applied
			}()

			select {
			case applied := <-second:
				t.Fatalf("the second removal answered (applied=%v) while the first was still "+
					"open, so both decided on the same picture", applied)
			case <-time.After(300 * time.Millisecond):
				// Blocked, as it should be.
			}

			require.NoError(t, tx1.Commit())

			select {
			case applied := <-second:
				require.False(t, applied,
					"the second removal applied too, leaving the instance with no administrator")
			case <-time.After(5 * time.Second):
				t.Fatal("the second removal never finished after the first committed")
			}

			require.Equal(t, 1, activeAdmins(t, db),
				"the instance should have exactly one administrator left")
		})
	}
}

func freshDatabase(t *testing.T) (*testutil.DB, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	name := "ghd_lastadmin_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	t.Setenv("TEST_DATABASE_URL", u.String())

	db, closeDB := testutil.NewDB(t)
	return db, func() {
		closeDB()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	}
}

func seedAdmin(t *testing.T, store *userstore.Store) uuid.UUID {
	t.Helper()
	u := user.User{
		ID:          uuid.New(),
		Email:       "admin-race-" + uuid.NewString() + "@test.local",
		DisplayName: "Race Admin",
		Role:        user.RoleAdmin,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, store.Create(context.Background(), u))
	return u.ID
}

// activeAdmins counts every administrator who could still log in. The whole
// database, because that is what the guard is protecting.
func activeAdmins(t *testing.T, db *testutil.DB) int {
	t.Helper()
	var n int
	err := db.SQL.QueryRow(
		`SELECT count(*) FROM users
		 WHERE role = 'admin' AND deleted_at IS NULL AND disabled = FALSE`).Scan(&n)
	require.NoError(t, err)
	return n
}

// The guarded writes act on live rows only.
//
// Each statement counted the OTHER administrators as live ones — deleted_at
// IS NULL AND disabled = FALSE — but matched the row it then wrote on id
// alone. So an administrator holding a soft-deleted account's id could still
// act on it, and SetUserRoleUnlessLastAdmin would mark a deleted row 'admin':
// a deleted-but-administrator account sitting in the table.
//
// It could not tip the live-administrator count either way, because a deleted
// row was never counted, and nothing in internal/server restores one today.
// Closed anyway. A guarantee that holds only because no restore path happens
// to exist is one that breaks the day somebody writes the restore.
//
// Found by the session-B review of #292.
func TestGuardedWrites_RefuseADeletedTarget(t *testing.T) {
	db, closeDB := freshDatabase(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))
	ctx := context.Background()

	// Two administrators, so the last-admin guard is not what refuses.
	seedAdmin(t, store)
	gone := seedAdmin(t, store)

	applied, err := store.SoftDeleteUnlessLastAdmin(ctx, gone)
	require.NoError(t, err)
	require.True(t, applied, "the delete itself should have been allowed")

	t.Run("demoting a deleted account is refused", func(t *testing.T) {
		applied, err := store.SetRoleUnlessLastAdmin(ctx, gone, "user")
		require.NoError(t, err)
		require.False(t, applied)
	})

	// The one that produced a deleted row marked admin.
	t.Run("promoting a deleted account is refused", func(t *testing.T) {
		applied, err := store.SetRoleUnlessLastAdmin(ctx, gone, "admin")
		require.NoError(t, err)
		require.False(t, applied, "a soft-deleted account was promoted to administrator")

		var role string
		var deleted *time.Time
		require.NoError(t, db.SQL.QueryRow(
			`SELECT role, deleted_at FROM users WHERE id = $1`, gone).Scan(&role, &deleted))
		require.NotNil(t, deleted, "the row stopped being deleted")
		require.Equal(t, "admin", role,
			"this fixture was already an administrator; if this ever reads differently the test is not testing what it says")
	})

	t.Run("disabling a deleted account is refused", func(t *testing.T) {
		applied, err := store.DisableUnlessLastAdmin(ctx, gone)
		require.NoError(t, err)
		require.False(t, applied)
	})

	t.Run("deleting it twice is refused", func(t *testing.T) {
		applied, err := store.SoftDeleteUnlessLastAdmin(ctx, gone)
		require.NoError(t, err)
		require.False(t, applied)
	})

	// And a live account is still perfectly actionable, or this would "fix"
	// the hole by breaking administration.
	t.Run("a live account is untouched by any of this", func(t *testing.T) {
		live := seedAdmin(t, store)
		applied, err := store.SetRoleUnlessLastAdmin(ctx, live, "staff")
		require.NoError(t, err)
		require.True(t, applied, "a live account could no longer be demoted")
	})
}
