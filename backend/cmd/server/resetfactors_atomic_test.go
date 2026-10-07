package main

import (
	"context"
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

// reset-factors is all or nothing (#307 item 4).
//
// It used to run three statements: delete the passkeys, clear the TOTP
// columns, delete the sessions. A failure after the second left an account
// with no factor and its MFAPassed=true sessions alive, which is the state the
// session revocation exists to prevent.
//
// The failure is injected where the old order was weakest: a trigger makes the
// session delete fail. Everything the command had already done must be as it
// was, so the same command can simply be run again.
func TestResetFactors_AFailureEndingSessionsLeavesTheFactorsInPlace(t *testing.T) {
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

	email := "half-reset-" + uuid.NewString() + "@test.local"
	u := user.User{
		ID: uuid.New(), Email: email, DisplayName: "Locked Out Admin",
		Role: user.RoleAdmin, PasswordHash: "$2a$10$unchanged",
		MFASecret: "THEIRLOSTSECRET", MFAEnabled: true,
	}
	require.NoError(t, users.Create(ctx, u))
	require.NoError(t, passkeys.Create(ctx, webauthn.Credential{
		UserID: u.ID, CredentialID: []byte("key-" + uuid.NewString()), PublicKey: []byte("pk"), Transports: []string{"usb"},
	}))
	require.NoError(t, q.UpsertSession(ctx, dbgen.UpsertSessionParams{
		ID: "sess-" + uuid.NewString(), UserID: database.NullUUID(&u.ID),
		Data: []byte("{}"), LifetimeSeconds: 3600,
	}))

	// The database is this test's own (freshDatabase), so the trigger cannot
	// reach another test.
	_, err = sqlDB.ExecContext(ctx, `
		CREATE FUNCTION refuse_session_delete() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected failure ending sessions'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER refuse_session_delete BEFORE DELETE ON sessions
		FOR EACH ROW EXECUTE FUNCTION refuse_session_delete();`)
	require.NoError(t, err)

	require.Error(t, resetFactors(ctx, email), "the failure must be reported, not swallowed")

	after, err := users.GetByIDAdmin(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, after.MFAEnabled, "the authenticator was cleared though ending the sessions failed")
	require.Equal(t, "THEIRLOSTSECRET", after.MFASecret)
	n, err := passkeys.CountForUser(ctx, u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the passkey was deleted though ending the sessions failed")
}
