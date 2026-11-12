package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// An OAuth client and its service user are written and removed together (#420).

func newClient(name, clientID string) auth.OAuthClient {
	return auth.OAuthClient{
		ID: uuid.New(), ClientID: clientID, HashedSecret: "h", Name: name,
		Scopes: []string{"tickets:read"}, CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UserID: uuid.New(),
	}
}

func TestAuthStore_CreateClientMakesItsServiceUser(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as, us := authstore.New(q), userstore.New(q)

	c := newClient("Nagios", "c-"+uuid.NewString())
	require.NoError(t, as.CreateOAuthClient(ctx, c))

	u, err := us.GetByIDAdmin(ctx, c.UserID)
	require.NoError(t, err)
	require.True(t, u.ServiceAccount)
	require.Equal(t, user.RoleStaff, u.Role)
	require.Equal(t, "Nagios", u.DisplayName, "named after the client")
	require.Empty(t, u.PasswordHash)
	require.False(t, u.MFAEnabled)

	got, gotUser, err := as.GetWithUser(ctx, c.ClientID)
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID)
	require.Equal(t, c.UserID, got.UserID)
	require.Equal(t, c.UserID, gotUser.ID)
}

// The user and the client are one statement, so a client that cannot be
// written leaves no user behind. Forced here with a duplicate client_id, which
// the second half of the statement refuses.
func TestAuthStore_CreateClientIsAtomic(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()
	as, us := authstore.New(q), userstore.New(q)

	first := newClient("First", "dup-"+uuid.NewString())
	require.NoError(t, as.CreateOAuthClient(ctx, first))

	// A failed statement aborts a Postgres transaction, so the refused create
	// runs inside a savepoint and the check reads after rolling back to it.
	_, err := tx.ExecContext(ctx, `SAVEPOINT refused`)
	require.NoError(t, err)
	second := newClient("Second", first.ClientID)
	require.Error(t, as.CreateOAuthClient(ctx, second))
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT refused`)
	require.NoError(t, err)

	_, err = us.GetByIDAdmin(ctx, second.UserID)
	require.ErrorIs(t, err, user.ErrNotFound, "a refused client must not leave its user behind")
}

func TestAuthStore_DeleteClientSoftDeletesItsUser(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as, us := authstore.New(q), userstore.New(q)

	c := newClient("Nagios", "c-"+uuid.NewString())
	require.NoError(t, as.CreateOAuthClient(ctx, c))

	require.NoError(t, as.DeleteOAuthClient(ctx, c.ID))

	_, _, err := as.GetWithUser(ctx, c.ClientID)
	require.Error(t, err, "the client is gone, so its tokens stop working")

	u, err := us.GetByIDAdmin(ctx, c.UserID)
	require.NoError(t, err, "the user is kept, so what it wrote keeps its author")
	require.NotNil(t, u.DeletedAt)
	require.Equal(t, "Nagios", u.DisplayName)

	_, err = us.GetByID(ctx, c.UserID)
	require.ErrorIs(t, err, user.ErrNotFound, "and it is no longer a live account")

	require.ErrorIs(t, as.DeleteOAuthClient(ctx, c.ID), auth.ErrNotFound, "a second delete is a 404")
}

// One query for every client's groups, not one per row.
func TestAuthStore_ListClientGroups(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as, gs := authstore.New(q), groupstore.New(q)

	withGroups := newClient("With groups", "c-"+uuid.NewString())
	without := newClient("Without", "c-"+uuid.NewString())
	require.NoError(t, as.CreateOAuthClient(ctx, withGroups))
	require.NoError(t, as.CreateOAuthClient(ctx, without))

	g1 := group.Group{ID: uuid.New(), Name: "B group " + uuid.NewString()}
	g2 := group.Group{ID: uuid.New(), Name: "A group " + uuid.NewString()}
	require.NoError(t, gs.Create(ctx, g1))
	require.NoError(t, gs.Create(ctx, g2))
	require.NoError(t, gs.AddMember(ctx, g1.ID, withGroups.UserID))
	require.NoError(t, gs.AddMember(ctx, g2.ID, withGroups.UserID))

	groups, err := as.ListOAuthClientGroups(ctx)
	require.NoError(t, err)
	require.Equal(t, []auth.ClientGroup{{ID: g2.ID, Name: g2.Name}, {ID: g1.ID, Name: g1.Name}},
		groups[withGroups.ID], "by group name")
	require.Empty(t, groups[without.ID])

	names, err := as.ListOAuthClientsWithoutGroups(ctx)
	require.NoError(t, err)
	require.Contains(t, names, "Without")
	require.NotContains(t, names, "With groups")
}

// A switched-off client is not a warning: the point of the warning is a
// client that will act and see nothing.
func TestAuthStore_ClientsWithoutGroupsSkipsDisabledUsers(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()
	as, us := authstore.New(q), userstore.New(q)

	c := newClient("Switched off "+uuid.NewString(), "c-"+uuid.NewString())
	require.NoError(t, as.CreateOAuthClient(ctx, c))
	names, err := as.ListOAuthClientsWithoutGroups(ctx)
	require.NoError(t, err)
	require.Contains(t, names, c.Name)

	require.NoError(t, us.Disable(ctx, c.UserID))
	names, err = as.ListOAuthClientsWithoutGroups(ctx)
	require.NoError(t, err)
	require.NotContains(t, names, c.Name)
}
