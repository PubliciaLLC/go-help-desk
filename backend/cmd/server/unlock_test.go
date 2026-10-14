package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
)

// unlockFixture is a database of its own with the server's environment set,
// and handles onto it.
type unlockFixture struct {
	sqlDB *sql.DB
	q     *dbgen.Queries
	users *userstore.Store
}

func newUnlockFixture(t *testing.T) *unlockFixture {
	t.Helper()
	dsn := freshDatabase(t)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("SESSION_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")

	pool, err := database.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	q := dbgen.New(sqlDB)
	return &unlockFixture{sqlDB: sqlDB, q: q, users: userstore.New(q)}
}

// lockedOutAdmin is every kind of locked out at once: disabled, a lost
// authenticator and passkey, a spent MFA budget with a lock, and a session.
func (f *unlockFixture) lockedOutAdmin(t *testing.T) (user.User, string) {
	t.Helper()
	ctx := context.Background()
	u := user.User{
		ID: uuid.New(), Email: "locked-" + uuid.NewString() + "@test.local", DisplayName: "Locked Out Admin",
		Role: user.RoleAdmin, PasswordHash: "$2a$10$oldhash",
		MFASecret: "THEIRLOSTSECRET", MFAEnabled: true, Disabled: true,
	}
	require.NoError(t, f.users.Create(ctx, u))
	require.NoError(t, webauthnstore.New(f.q).Create(ctx, webauthn.Credential{
		UserID: u.ID, CredentialID: []byte("key-" + uuid.NewString()), PublicKey: []byte("pk"), Transports: []string{"usb"},
	}))
	_, err := f.sqlDB.ExecContext(ctx,
		`UPDATE users SET disabled = true, mfa_failed_attempts = 5, mfa_locked_until = now() + interval '1 hour' WHERE id = $1`, u.ID)
	require.NoError(t, err)
	sid := "sess-" + uuid.NewString()
	require.NoError(t, f.q.UpsertSession(ctx, dbgen.UpsertSessionParams{
		ID: sid, UserID: database.NullUUID(&u.ID), Data: []byte("{}"), LifetimeSeconds: 3600,
	}))
	return u, sid
}

// sessionRows counts the rows directly. GetSession hides a disabled user's
// sessions, which would make "it was ended" true of every session here.
func (f *unlockFixture) sessionRows(t *testing.T, id string) int {
	t.Helper()
	var n int
	require.NoError(t, f.sqlDB.QueryRow(`SELECT count(*) FROM sessions WHERE id = $1`, id).Scan(&n))
	return n
}

// printedPassword pulls the password out of the command's output, from the
// line an operator would copy it from.
func printedPassword(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "  New password:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no password in the output:\n%s", out)
	return ""
}

func TestUnlock_AppliesEveryEffectInOneGo(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()
	u, sid := f.lockedOutAdmin(t)

	other := user.User{ID: uuid.New(), Email: "bystander-" + uuid.NewString() + "@test.local",
		DisplayName: "Bystander", Role: user.RoleStaff, PasswordHash: "$2a$10$x"}
	require.NoError(t, f.users.Create(ctx, other))
	otherSID := "sess-" + uuid.NewString()
	require.NoError(t, f.q.UpsertSession(ctx, dbgen.UpsertSessionParams{
		ID: otherSID, UserID: database.NullUUID(&other.ID), Data: []byte("{}"), LifetimeSeconds: 3600,
	}))

	var out bytes.Buffer
	require.NoError(t, unlock(ctx, u.Email, &out))

	after, err := f.users.GetByIDAdmin(ctx, u.ID)
	require.NoError(t, err)
	require.False(t, after.MFAEnabled, "the authenticator survived")
	require.Empty(t, after.MFASecret)
	n, err := webauthnstore.New(f.q).CountForUser(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, n, "a passkey survived")
	require.False(t, after.Disabled, "the account is still disabled")
	require.True(t, after.MustChangePassword, "the new password does not have to be changed")
	require.Equal(t, user.RoleAdmin, after.Role, "it changed the role")

	attempts, lockedUntil, err := f.users.GetMFALock(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, attempts, "the MFA failure count was not cleared")
	require.Nil(t, lockedUntil, "the MFA lock was not cleared")

	require.Zero(t, f.sessionRows(t, sid), "a session survived")
	require.Equal(t, 1, f.sessionRows(t, otherSID), "another account's session was ended")

	// The password: random, long enough, current cost, and the one printed.
	pw := printedPassword(t, out.String())
	require.GreaterOrEqual(t, len(pw), 20)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte(pw)))
	cost, err := bcrypt.Cost([]byte(after.PasswordHash))
	require.NoError(t, err)
	require.Equal(t, user.ProductionBcryptCost, cost)

	o, err := f.users.GetByIDAdmin(ctx, other.ID)
	require.NoError(t, err)
	require.Equal(t, "$2a$10$x", o.PasswordHash)
	require.False(t, o.MustChangePassword)

	// One audit entry, actor: the server console, and no secret in it.
	entries, err := auditstore.New(f.q).ListByEntity(ctx, "user", u.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	require.Nil(t, e.ActorID)
	require.Equal(t, "account_unlock", e.Action)
	require.Equal(t, "server console", e.After["actor"])
	require.Equal(t, "cli", e.After["source"])
	require.NotEmpty(t, e.After["host"])
	require.NotEmpty(t, e.After["os_user"])
	require.NotContains(t, fmt.Sprint(e.After), pw)
}

// The password exists in exactly one place: the terminal, once. Two runs give
// two different passwords.
func TestUnlock_PrintsThePasswordOnceAndNeverLogsIt(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()
	u, _ := f.lockedOutAdmin(t)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var out bytes.Buffer
	require.NoError(t, unlock(ctx, u.Email, &out))
	pw := printedPassword(t, out.String())
	require.Equal(t, 1, strings.Count(out.String(), pw), "the password was printed more than once")
	require.NotContains(t, logs.String(), pw, "the password reached the log")

	var again bytes.Buffer
	require.NoError(t, unlock(ctx, u.Email, &again))
	require.NotEqual(t, pw, printedPassword(t, again.String()), "two runs produced the same password")
}

func TestUnlock_RefusesWhatItCannotUnlock(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()

	deleted := user.User{ID: uuid.New(), Email: "gone-" + uuid.NewString() + "@test.local",
		DisplayName: "Deleted", Role: user.RoleStaff, PasswordHash: "$2a$10$keep"}
	require.NoError(t, f.users.Create(ctx, deleted))
	require.NoError(t, f.users.SoftDelete(ctx, deleted.ID))

	for name, email := range map[string]string{
		"unknown": "nobody-" + uuid.NewString() + "@test.local",
		"empty":   "",
		"deleted": deleted.Email,
	} {
		var out bytes.Buffer
		require.Error(t, unlock(ctx, email, &out), name)
		require.Empty(t, out.String(), "%s: printed something though it refused", name)
	}

	got, err := f.users.GetByIDAdmin(ctx, deleted.ID)
	require.NoError(t, err)
	require.Equal(t, "$2a$10$keep", got.PasswordHash, "a deleted account had its password set")
	require.False(t, got.MustChangePassword)
	// TODO(#490): refuse an OAuth service account here once that field is on
	// this branch's base, and test it.
}

// While SAML is on, only administrators may sign in with a password
// (user.IsLocalAuthAllowed). The unlock still happens, but the operator is told
// the password cannot be used, rather than left to find out at the login page.
func TestUnlock_SaysSoWhenThePasswordCannotSignThisAccountIn(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()
	require.NoError(t, admin.NewService(adminstore.New(f.q)).SetSAMLEnabled(ctx, true))

	staff := user.User{ID: uuid.New(), Email: "staff-" + uuid.NewString() + "@test.local",
		DisplayName: "Staff", Role: user.RoleStaff, PasswordHash: "$2a$10$x"}
	require.NoError(t, f.users.Create(ctx, staff))
	adm := user.User{ID: uuid.New(), Email: "adm-" + uuid.NewString() + "@test.local",
		DisplayName: "Adm", Role: user.RoleAdmin, PasswordHash: "$2a$10$x"}
	require.NoError(t, f.users.Create(ctx, adm))

	var out bytes.Buffer
	require.NoError(t, unlock(ctx, staff.Email, &out))
	require.Contains(t, out.String(), "SAML")
	require.Contains(t, out.String(), "The password above will not get them in")
	require.NotEmpty(t, printedPassword(t, out.String()))

	out.Reset()
	require.NoError(t, unlock(ctx, adm.Email, &out))
	require.NotContains(t, out.String(), "The password above will not get them in")
}

// All or nothing, and nothing printed: a password the database never stored
// is worse than none.
func TestUnlock_AFailureLeavesTheAccountAsItWasAndPrintsNothing(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()
	u, sid := f.lockedOutAdmin(t)

	// Fails at the audit write, the last step, so everything before it has
	// already run inside the transaction.
	_, err := f.sqlDB.ExecContext(ctx, `
		CREATE FUNCTION refuse_audit() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected failure writing audit'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_log
		FOR EACH ROW EXECUTE FUNCTION refuse_audit();`)
	require.NoError(t, err)

	var out bytes.Buffer
	require.Error(t, unlock(ctx, u.Email, &out))
	require.Empty(t, out.String(), "a password was printed for a change that rolled back")

	after, err := f.users.GetByIDAdmin(ctx, u.ID)
	require.NoError(t, err)
	require.True(t, after.Disabled)
	require.True(t, after.MFAEnabled)
	require.Equal(t, "$2a$10$oldhash", after.PasswordHash)
	require.False(t, after.MustChangePassword)
	require.Equal(t, 1, f.sessionRows(t, sid), "the session was ended though the unlock rolled back")
	attempts, _, err := f.users.GetMFALock(ctx, u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 5, attempts)
}

// reset-factors stays exactly what it was: factors and sessions, nothing else.
func TestResetFactors_StillLeavesThePasswordAndTheFlagAlone(t *testing.T) {
	f := newUnlockFixture(t)
	ctx := context.Background()
	u, _ := f.lockedOutAdmin(t)

	require.NoError(t, resetFactors(ctx, u.Email))

	after, err := f.users.GetByIDAdmin(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "$2a$10$oldhash", after.PasswordHash)
	require.False(t, after.MustChangePassword)
	require.True(t, after.Disabled, "reset-factors re-enabled the account; that is unlock's job")
	attempts, _, err := f.users.GetMFALock(ctx, u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 5, attempts, "reset-factors cleared the MFA lock; that is unlock's job")
}
