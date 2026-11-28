package userstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Service users are the identities OAuth clients act as (#420). They never
// sign in, and the store is where that is enforced against a real database:
// the login lookups do not see them, and the writes that would give one a way
// in are refused as ErrServiceAccount.

// newServiceUserFixture returns a store over a rolled-back transaction, one
// service user in it, and a maker of ordinary accounts.
func newServiceUserFixture(t *testing.T) (*userstore.Store, user.User, func(t *testing.T) user.User) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)
	store := userstore.New(q)
	ctx := context.Background()

	now := time.Now().UTC()
	svc := user.User{
		ID: uuid.New(), Email: "oauth-" + uuid.NewString() + "@service-account.invalid",
		DisplayName: "Nagios", Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.Create(ctx, svc))
	_, err := tx.ExecContext(ctx, `UPDATE users SET service_account = TRUE WHERE id = $1`, svc.ID)
	require.NoError(t, err)
	svc.ServiceAccount = true

	person := func(t *testing.T) user.User {
		t.Helper()
		p := user.User{
			ID: uuid.New(), Email: "person-" + uuid.NewString() + "@test.local",
			DisplayName: "Person", Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, store.Create(ctx, p))
		return p
	}
	return store, svc, person
}

func TestServiceUser_LoginLookupsDoNotSeeIt(t *testing.T) {
	store, svc, person := newServiceUserFixture(t)
	ctx := context.Background()

	_, err := store.GetByEmail(ctx, svc.Email)
	require.ErrorIs(t, err, user.ErrNotFound, "password login and SSO adoption look up by email")

	// The control: an ordinary account is still found by the same query.
	p := person(t)
	got, err := store.GetByEmail(ctx, p.Email)
	require.NoError(t, err)
	require.Equal(t, p.ID, got.ID)

	// Nothing is hidden from the id lookup the admin pages use.
	byID, err := store.GetByIDAdmin(ctx, svc.ID)
	require.NoError(t, err)
	require.True(t, byID.ServiceAccount)

	// The address stays taken: signup must not be able to claim it.
	taken, err := store.EmailIsTaken(ctx, svc.Email)
	require.NoError(t, err)
	require.True(t, taken)
}

func TestServiceUser_PasswordLoginFails(t *testing.T) {
	store, svc, _ := newServiceUserFixture(t)
	service := user.NewService(store, user.WithBcryptCost(4))

	_, err := service.VerifyPassword(context.Background(), svc.Email, "anything-at-all")
	require.Error(t, err)
}

func TestServiceUser_WritesThatGiveAWayInAreRefused(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		do   func(store *userstore.Store, u user.User) error
	}{
		{"admin set password", func(st *userstore.Store, u user.User) error { return st.AdminSetPassword(ctx, u.ID, "$2a$10$x") }},
		{"set password hash", func(st *userstore.Store, u user.User) error { return st.SetPasswordHash(ctx, u.ID, "$2a$10$x") }},
		{"set MFA", func(st *userstore.Store, u user.User) error { return st.SetMFA(ctx, u.ID, "SECRET", true) }},
		{"first MFA", func(st *userstore.Store, u user.User) error {
			_, err := st.SetFirstMFA(ctx, u.ID, "SECRET")
			return err
		}},
		{"promote to admin", func(st *userstore.Store, u user.User) error {
			_, err := st.SetRoleUnlessLastAdmin(ctx, u.ID, "admin")
			return err
		}},
		{"whole-row update with a password", func(st *userstore.Store, u user.User) error {
			u.PasswordHash = "$2a$10$x"
			return st.Update(ctx, u)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A refused statement aborts the Postgres transaction, so each
			// case gets its own fixture.
			store, svc, _ := newServiceUserFixture(t)
			require.ErrorIs(t, tc.do(store, svc), user.ErrServiceAccount)
		})
	}
}

// A service user is deleted by deleting its client, never on its own: the
// guarded delete does not apply to it, whoever calls.
func TestServiceUser_CannotBeSoftDeletedDirectly(t *testing.T) {
	store, svc, person := newServiceUserFixture(t)
	ctx := context.Background()
	applied, err := store.SetRoleUnlessLastAdmin(ctx, person(t).ID, "admin")
	require.NoError(t, err)
	require.True(t, applied)

	applied, err = store.SoftDeleteUnlessLastAdmin(ctx, svc.ID)
	require.NoError(t, err)
	require.False(t, applied)

	got, err := store.GetByID(ctx, svc.ID)
	require.NoError(t, err)
	require.Nil(t, got.DeletedAt)

	// And the service says why, not "this is the only administrator".
	err = user.NewService(store).SoftDelete(ctx, svc.ID)
	require.ErrorIs(t, err, user.ErrServiceAccount)
}

func TestServiceUser_MayBeRenamedAndMovedBetweenStaffAndUser(t *testing.T) {
	store, svc, person := newServiceUserFixture(t)
	ctx := context.Background()

	// Every guarded write wants one other administrator to exist.
	applied, err := store.SetRoleUnlessLastAdmin(ctx, person(t).ID, "admin")
	require.NoError(t, err)
	require.True(t, applied)

	require.NoError(t, store.UpdateProfile(ctx, svc.ID, svc.Email, "Nagios (prod)"))
	applied, err = store.SetRoleUnlessLastAdmin(ctx, svc.ID, "user")
	require.NoError(t, err)
	require.True(t, applied)

	got, err := store.GetByIDAdmin(ctx, svc.ID)
	require.NoError(t, err)
	require.Equal(t, "Nagios (prod)", got.DisplayName)
	require.Equal(t, user.RoleUser, got.Role)
}

func TestServiceUser_CannotBeAdoptedBySSO(t *testing.T) {
	store, svc, _ := newServiceUserFixture(t)
	applied, err := store.AdoptOIDCSubject(context.Background(), svc.ID, "sub-"+uuid.NewString(), "Mallory")
	require.NoError(t, err)
	require.False(t, applied)
}

// The last-administrator guard counts role = 'admin', and the CHECK means a
// service user can never be one, so it neither props the guard up nor trips it.
func TestServiceUser_IsNotAnAdministratorForTheLastAdminGuard(t *testing.T) {
	store, svc, person := newServiceUserFixture(t)
	ctx := context.Background()

	admins, err := store.ListActiveAdmins(ctx)
	require.NoError(t, err)
	for _, a := range admins {
		require.NotEqual(t, svc.ID, a.ID)
	}

	// One real administrator exists, so the guard lets the service user be
	// disabled; and the count of administrators does not move.
	adm := person(t)
	applied, err := store.SetRoleUnlessLastAdmin(ctx, adm.ID, "admin")
	require.NoError(t, err)
	require.True(t, applied)

	before, err := store.CountOtherActiveAdmins(ctx, svc.ID)
	require.NoError(t, err)

	applied, err = store.DisableUnlessLastAdmin(ctx, svc.ID)
	require.NoError(t, err)
	require.True(t, applied)

	after, err := store.CountOtherActiveAdmins(ctx, svc.ID)
	require.NoError(t, err)
	require.Equal(t, before, after, "disabling a service user changes no administrator count")
}

// Everything that is a list of colleagues leaves the service user out; the
// administrator's list (ListAdmin) keeps it, so it can be edited.
func TestServiceUser_ListedForAdministratorsOnly(t *testing.T) {
	store, svc, person := newServiceUserFixture(t)
	ctx := context.Background()
	p := person(t)

	staff, err := store.ListAssignableStaff(ctx)
	require.NoError(t, err)
	ids := func() (out []uuid.UUID) {
		for _, s := range staff {
			out = append(out, s.ID)
		}
		return out
	}()
	require.Contains(t, ids, p.ID)
	require.NotContains(t, ids, svc.ID)

	plain, err := store.List(ctx, 1000, 0)
	require.NoError(t, err)
	var plainIDs []uuid.UUID
	for _, u := range plain {
		plainIDs = append(plainIDs, u.ID)
	}
	require.Contains(t, plainIDs, p.ID)
	require.NotContains(t, plainIDs, svc.ID)

	admin, err := store.ListAdmin(ctx, 1000, 0)
	require.NoError(t, err)
	var adminIDs []uuid.UUID
	for _, u := range admin {
		adminIDs = append(adminIDs, u.ID)
	}
	require.Contains(t, adminIDs, svc.ID)
}
