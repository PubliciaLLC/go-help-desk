package userstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// queryCounter counts the statements that read rows, so a test can say how many
// round trips an operation took.
type queryCounter struct {
	*sql.DB
	n atomic.Int32
}

func (c *queryCounter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.DB.QueryContext(ctx, q, args...)
}

func (c *queryCounter) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.n.Add(1)
	return c.DB.QueryRowContext(ctx, q, args...)
}

// #444: the requester picker's lookup is one bounded query. The accounts that
// cannot be requesters (disabled, deleted, service) are excluded by the query,
// not read and dropped afterwards, so a search whose matches are mostly
// disabled costs the same as any other, and the order is total, so nothing is
// answered twice.
func TestLookupRequesters_OneQueryTenEnabledAccounts(t *testing.T) {
	db, closeDB := freshDatabase(t)
	defer closeDB()
	ctx := context.Background()
	counter := &queryCounter{DB: db.SQL}
	store := userstore.New(dbgen.New(counter))

	// 60 matches, only the multiples of 7 enabled (9 of them); and one match
	// each that is deleted and a service identity, which are never found.
	for i := 0; i < 60; i++ {
		u := user.User{
			ID: uuid.New(), Email: fmt.Sprintf("find%02d@test.local", i), DisplayName: fmt.Sprintf("Findable %02d", i),
			Role: user.RoleUser, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		require.NoError(t, store.Create(ctx, u))
	}
	// (Create does not take the flag; Disable is the way an account gets it.)
	for i := 0; i < 60; i++ {
		if i%7 != 0 {
			u, err := store.GetByEmail(ctx, fmt.Sprintf("find%02d@test.local", i))
			require.NoError(t, err)
			require.NoError(t, store.Disable(ctx, u.ID))
		}
	}
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, deleted_at)
		VALUES (gen_random_uuid(), 'find-deleted@test.local', 'Findable Deleted', 'user', now())`)
	require.NoError(t, err)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES (gen_random_uuid(), 'find-svc@test.local', 'Findable Service', 'staff', true)`)
	require.NoError(t, err)

	counter.n.Store(0)
	got, err := store.LookupRequesters(ctx, "findable")
	require.NoError(t, err)
	require.EqualValues(t, 1, counter.n.Load(), "one query, however many matches are disabled")

	var names []string
	seen := map[uuid.UUID]bool{}
	for _, u := range got {
		require.False(t, seen[u.ID], "%s twice", u.Email)
		seen[u.ID] = true
		require.False(t, u.Disabled)
		names = append(names, u.DisplayName)
	}
	require.Equal(t, []string{
		"Findable 00", "Findable 07", "Findable 14", "Findable 21", "Findable 28",
		"Findable 35", "Findable 42", "Findable 49", "Findable 56",
	}, names, "all nine enabled accounts, by display name")

	// And the cap is the query's own: more enabled matches than ten still
	// answers ten.
	for i := 60; i < 80; i++ {
		require.NoError(t, store.Create(ctx, user.User{
			ID: uuid.New(), Email: fmt.Sprintf("find%02d@test.local", i), DisplayName: fmt.Sprintf("Findable %02d", i),
			Role: user.RoleUser, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}))
	}
	got, err = store.LookupRequesters(ctx, "findable")
	require.NoError(t, err)
	require.Len(t, got, 10)

	// LIKE wildcards in q are text, as in the admin search.
	got, err = store.LookupRequesters(ctx, "%%")
	require.NoError(t, err)
	require.Empty(t, got)
}
