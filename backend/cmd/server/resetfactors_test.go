package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
)

// reset-factors is the way back from a lockout the web path must refuse to
// resolve.
//
// A session that has not passed MFA cannot add or remove a factor on an
// account that already has one, because otherwise a password alone is enough
// to replace somebody's second factor. Correct, and it leaves an
// administrator whose key is lost dependent on another administrator — of
// which a sole administrator has none. Measured before this existed: every
// door answered 403 and the account reached nothing.
func TestResetFactors_ClearsEveryFactorAndNothingElse(t *testing.T) {
	dsn := freshDatabase(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	ctx := context.Background()

	pool, err := database.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	q := dbgen.New(sqlDB)
	users := userstore.New(q)
	passkeys := webauthnstore.New(q)

	email := "locked-out-" + uuid.NewString() + "@test.local"
	u := user.User{
		ID: uuid.New(), Email: email, DisplayName: "Locked Out Admin",
		Role: user.RoleAdmin, PasswordHash: "$2a$10$unchanged",
		MFASecret: "THEIRLOSTSECRET", MFAEnabled: true,
	}
	require.NoError(t, users.Create(ctx, u))

	require.NoError(t, passkeys.Create(ctx, webauthn.Credential{
		UserID: u.ID, CredentialID: []byte("lost-key-" + uuid.NewString()),
		PublicKey: []byte("pk"), Transports: []string{"usb"},
	}))

	require.NoError(t, resetFactors(ctx, email))

	after, err := users.GetByIDAdmin(ctx, u.ID)
	require.NoError(t, err)
	require.False(t, after.MFAEnabled, "the authenticator is still enrolled")
	require.Empty(t, after.MFASecret, "the secret survived, so the lost authenticator still works")

	n, err := passkeys.CountForUser(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, n, "a registered passkey survived, so the account is still locked")

	// And nothing else moved. This is a recovery tool, not an account editor:
	// it must not quietly change a password, a role, or whether the account
	// is enabled.
	require.Equal(t, "$2a$10$unchanged", after.PasswordHash, "it changed the password")
	require.Equal(t, user.RoleAdmin, after.Role, "it changed the role")
	require.False(t, after.Disabled, "it disabled the account")
	require.Nil(t, after.DeletedAt, "it deleted the account")
	require.Equal(t, email, after.Email)
}

// A typo must not reset somebody. The lookup is exact.
func TestResetFactors_RefusesAnAddressItCannotFind(t *testing.T) {
	dsn := freshDatabase(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")

	require.Error(t, resetFactors(context.Background(), "nobody-"+uuid.NewString()+"@test.local"))
	require.Error(t, resetFactors(context.Background(), ""), "an empty address was accepted")
}

// freshDatabase gives this test its own database.
//
// Not tidiness: the setup route decides whether an instance has ever been
// configured by counting EVERY row in users, and Go runs packages in
// parallel. A user created here exists while internal/server's setup tests
// are asking that question, so they fail with an error that says nothing
// about reset-factors. The first version of this file did exactly that.
// Cleaning up afterwards does not help — the collision is concurrent, not
// leftover. Same approach as userstore's last-admin tests.
func freshDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	name := "ghd_resetfactors_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	own := u.String()

	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		_ = admin.Close()
	})
	require.NoError(t, database.Migrate(context.Background(), database.MigrateURL(own)))
	return own
}
