package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

// oidcUser builds a user record carrying the given OIDC subject.
func oidcUser(email, subject string) user.User {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return user.User{
		ID:          uuid.New(),
		Email:       email,
		DisplayName: email,
		Role:        user.RoleUser,
		OIDCSubject: subject,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// TestUserStore_OIDCSubjectIsUnique pins defect 11.
//
// saml_subject has a partial index (000001_init); oidc_subject (000015) has
// none at all. Nothing stops two rows sharing a subject, and GetUserByOIDCSubject
// is a `:one` query — with duplicates present it silently returns whichever row
// Postgres hands back first, so a login can land on the wrong account.
//
// The index must be UNIQUE and partial (WHERE oidc_subject != ''), mirroring
// the SAML one, because '' is the "not federated" sentinel shared by every
// local account.
func TestUserStore_OIDCSubjectIsUnique(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, oidcUser("first@example.com", "duplicate-sub")))

	err := s.Create(ctx, oidcUser("second@example.com", "duplicate-sub"))
	require.Error(t, err,
		"a second user must not be able to claim an OIDC subject that is already bound")
}

// TestUserStore_OIDCSubjectAllowsManyEmpty is the other half of the constraint:
// every local-only account stores '', so the unique index must be partial.
func TestUserStore_OIDCSubjectAllowsManyEmpty(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, oidcUser("local-one@example.com", "")))
	require.NoError(t, s.Create(ctx, oidcUser("local-two@example.com", "")),
		"many accounts may have no OIDC subject")
	require.NoError(t, s.Create(ctx, oidcUser("local-three@example.com", "")))
}

// TestGetUserByOIDCSubject_ExcludesSoftDeleted verifies that a deleted account
// cannot be resurrected by an IdP login.
func TestGetUserByOIDCSubject_ExcludesSoftDeleted(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	u := oidcUser("deleted@example.com", "deleted-sub")
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByOIDCSubject(ctx, "deleted-sub")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)

	require.NoError(t, s.SoftDelete(ctx, u.ID))

	_, err = s.GetByOIDCSubject(ctx, "deleted-sub")
	require.ErrorIs(t, err, userstore.ErrNotFound,
		"a soft-deleted account must not be reachable by OIDC subject")
}

// TestGetUserByOIDCSubject_EmptySubjectNeverMatches guards the sentinel: looking
// up '' must not return one of the many local accounts that store it.
func TestGetUserByOIDCSubject_EmptySubjectNeverMatches(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, oidcUser("plain-local@example.com", "")))

	_, err := s.GetByOIDCSubject(ctx, "")
	require.ErrorIs(t, err, userstore.ErrNotFound,
		"an empty OIDC subject must never match an account")
}

// TestUserStore_OIDCSubjectRoundTrips is a plain read-back check: the column is
// written and returned as stored.
func TestUserStore_OIDCSubjectRoundTrips(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	s := userstore.New(q)
	ctx := context.Background()

	u := oidcUser("roundtrip@example.com", "roundtrip-sub")
	require.NoError(t, s.Create(ctx, u))

	byID, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "roundtrip-sub", byID.OIDCSubject)

	bySub, err := s.GetByOIDCSubject(ctx, "roundtrip-sub")
	require.NoError(t, err)
	require.Equal(t, u.ID, bySub.ID)
	require.Equal(t, user.RoleUser, bySub.Role)
}
