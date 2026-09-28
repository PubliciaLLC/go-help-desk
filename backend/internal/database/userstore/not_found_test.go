package userstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// ClearMFA and AdminSetPassword were sqlc :exec queries: an UPDATE matching
// zero rows reports no error, only "zero rows changed", which :exec discards.
// So a nonexistent target looked exactly like a successful clear or reset —
// domain code gated an audit entry on "did the store call error", and it
// never did. AdminSetPassword's HTTP handler has no re-fetch afterward, so
// this was reachable as a bare 204 "success" for an account that does not
// exist. Found by adversarial review of #306.
//
// :execrows plus a rows-affected check closes it at the one place both
// callers already share.
func TestClearMFA_ReportsNotFoundForANonexistentUser(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))

	err := store.ClearMFA(context.Background(), uuid.New())

	require.Error(t, err, "clearing MFA on an id that matches no row must fail, not silently succeed")
	require.True(t, errors.Is(err, user.ErrNotFound), "got %v, want it to wrap user.ErrNotFound", err)
}

func TestAdminSetPassword_ReportsNotFoundForANonexistentUser(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	store := userstore.New(dbgen.New(db.SQL))

	err := store.AdminSetPassword(context.Background(), uuid.New(), "$2a$10$somehashsomehashsomehashsomehashsomehash")

	require.Error(t, err, "resetting the password of an id that matches no row must fail, not silently succeed")
	require.True(t, errors.Is(err, user.ErrNotFound), "got %v, want it to wrap user.ErrNotFound", err)
}
