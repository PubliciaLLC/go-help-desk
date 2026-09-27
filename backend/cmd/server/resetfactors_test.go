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

// Clearing the factors must also end the sessions that were opened while they
// were in force.
//
// Found by the session-B review of #302. A live session carries MFAPassed=true
// from the moment it passed the factor that is now being cleared, and
// requireFactorOrFirstEnrolment answers that flag FIRST — before it asks
// whether the account is protected. So a session that survives this command
// can register its own passkey on the account.
//
// That is the shape this command exists to make unnecessary, arriving through
// the command itself: an attacker holding a stolen cookie is handed a durable
// factor by the act of recovering the real owner, and can then lock the owner
// out of their own recovery. The web-facing reset already deletes sessions for
// exactly this reason, four lines of reasoning away in handler_admin_users.go;
// this had the same duty and did not do it.
func TestResetFactors_RevokesTheSessionsOpenedUnderTheOldFactor(t *testing.T) {
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

	email := "stolen-cookie-" + uuid.NewString() + "@test.local"
	u := user.User{
		ID: uuid.New(), Email: email, DisplayName: "Locked Out Admin",
		Role: user.RoleAdmin, PasswordHash: "$2a$10$unchanged",
		MFASecret: "THEIRLOSTSECRET", MFAEnabled: true,
	}
	require.NoError(t, users.Create(ctx, u))

	// A second account, to prove this clears one person's sessions and not
	// everybody's — a recovery tool that signs out the whole instance is a
	// different kind of outage.
	other := user.User{
		ID: uuid.New(), Email: "bystander-" + uuid.NewString() + "@test.local",
		DisplayName: "Bystander", Role: user.RoleStaff, PasswordHash: "$2a$10$x",
	}
	require.NoError(t, users.Create(ctx, other))

	// Two devices for the locked-out account, one for the bystander.
	seed := func(id uuid.UUID, sid string) {
		require.NoError(t, q.UpsertSession(ctx, dbgen.UpsertSessionParams{
			ID:              sid,
			UserID:          database.NullUUID(&id),
			Data:            []byte("{}"),
			LifetimeSeconds: 3600,
		}))
	}
	laptop := "sess-laptop-" + uuid.NewString()
	phone := "sess-phone-" + uuid.NewString()
	bystander := "sess-bystander-" + uuid.NewString()
	seed(u.ID, laptop)
	seed(u.ID, phone)
	seed(other.ID, bystander)

	alive := func(sid string) bool {
		_, err := q.GetSession(ctx, sid)
		return err == nil
	}
	require.True(t, alive(laptop), "fixture: the session should exist before the reset")

	require.NoError(t, resetFactors(ctx, email))

	require.False(t, alive(laptop),
		"a session opened under the cleared factor survived, so whoever holds that cookie "+
			"can register their own passkey on the account")
	require.False(t, alive(phone), "the second device kept its session")
	require.True(t, alive(bystander),
		"another account's session was revoked; this resets one account, not the instance")
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
