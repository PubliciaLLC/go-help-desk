package user_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// fakeAuditStore is an in-memory audit.Store for unit tests.
type fakeAuditStore struct {
	entries []audit.Entry
	err     error // if set, Create returns this instead of recording
}

func (f *fakeAuditStore) Create(_ context.Context, e audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, e)
	return nil
}

func (f *fakeAuditStore) ListByEntity(context.Context, string, uuid.UUID, int, int) ([]audit.Entry, error) {
	return nil, nil
}

func (f *fakeAuditStore) Search(context.Context, audit.Filter, int, int) (audit.Page, error) {
	return audit.Page{}, nil
}

func (f *fakeAuditStore) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func seedActiveUser(store *fakeUserStore) user.User {
	u := user.User{
		ID:          uuid.New(),
		Email:       "target@example.com",
		DisplayName: "Target",
		Role:        user.RoleUser,
	}
	store.seed(u)
	return u
}

func TestResetMFA_RecordsWhoDidIt(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	target := seedActiveUser(store)
	actorID := uuid.New()

	svc := user.NewService(store, user.WithAuditStore(au))
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, &actorID))

	require.Len(t, au.entries, 1)
	e := au.entries[0]
	require.Equal(t, "user", e.EntityType)
	require.Equal(t, target.ID, e.EntityID)
	require.Equal(t, "mfa_reset", e.Action)
	require.NotNil(t, e.ActorID)
	require.Equal(t, actorID, *e.ActorID)
}

// "Reset MFA" cleared the TOTP columns and nothing else, so on a passkey-only
// account an administrator pressed it, got a success, and the person stayed
// locked out by the key they had lost (#307 item 2).
func TestResetMFA_ClearsPasskeysToo(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	target := seedActiveUser(store)
	store.passkeys[target.ID] = 2

	svc := user.NewService(store, user.WithAuditStore(au))
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, nil))

	require.Zero(t, store.passkeys[target.ID], "a passkey survived the reset")
	require.Len(t, au.entries, 1)
	require.Equal(t, 2, au.entries[0].After["passkeys_removed"],
		"the audit entry must say the reset covered passkeys, and how many")
}

func TestResetMFA_NilActorIsRecordedAsNoActor(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	target := seedActiveUser(store)

	svc := user.NewService(store, user.WithAuditStore(au))
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, nil))

	require.Len(t, au.entries, 1)
	require.Nil(t, au.entries[0].ActorID)
}

func TestResetMFA_WithoutAnAuditStoreStillWorks(t *testing.T) {
	store := newFakeUserStore()
	target := seedActiveUser(store)

	svc := user.NewService(store) // no WithAuditStore, exactly how most tests build it
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, nil))

	got, err := store.GetByID(context.Background(), target.ID)
	require.NoError(t, err)
	require.False(t, got.MFAEnabled)
}

func TestResetMFA_AuditWriteFailureDoesNotFailTheReset(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{err: errors.New("audit table is down")}
	target := seedActiveUser(store)

	svc := user.NewService(store, user.WithAuditStore(au))
	err := svc.ResetMFA(context.Background(), target.ID, nil)

	require.NoError(t, err, "the factor was actually cleared; a logging failure must not be reported as if it were not")
	got, storeErr := store.GetByID(context.Background(), target.ID)
	require.NoError(t, storeErr)
	require.False(t, got.MFAEnabled, "the reset itself must still have applied")
}

// The logger is an explicit dependency (WithLogger), not the global log/slog
// package — mirroring webauthn.Service. This confirms an audit write failure
// actually reaches the logger the caller configured, not slog.Default()'s
// destination the test cannot see.
func TestResetMFA_AuditWriteFailureIsLoggedToTheConfiguredLogger(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{err: errors.New("audit table is down")}
	target := seedActiveUser(store)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	svc := user.NewService(store, user.WithAuditStore(au), user.WithLogger(log))
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, nil))

	require.Contains(t, buf.String(), "audit table is down")
	require.Contains(t, buf.String(), "mfa_reset")
}

// WithLogger(nil) must behave exactly like omitting the option — the default
// (slog.Default()) stays in force — not panic and not silently discard
// failures. Swaps the process-wide default for the duration of the test so
// there is somewhere to observe it land, and restores it afterward.
func TestWithLogger_NilIsANoOpAndKeepsTheDefault(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	store := newFakeUserStore()
	au := &fakeAuditStore{err: errors.New("audit table is down")}
	target := seedActiveUser(store)

	svc := user.NewService(store, user.WithAuditStore(au), user.WithLogger(nil))
	require.NoError(t, svc.ResetMFA(context.Background(), target.ID, nil),
		"WithLogger(nil) must not panic or otherwise disturb the call")

	require.Contains(t, buf.String(), "audit table is down",
		"WithLogger(nil) must fall back to the default logger, not a nil one that discards the write")
}

// A store failure — including "no such row", which is what the real
// Postgres-backed store reports for a nonexistent target — must refuse the
// request and write nothing. The real store used to get this wrong (an
// UPDATE matching zero rows reported no error), so ResetMFA wrote an audit
// entry for an account that never existed; see userstore's ClearMFA/
// AdminSetPassword and #306's adversarial review. This pins the contract
// ResetMFA itself must hold regardless of what the store does.
func TestResetMFA_StoreFailureWritesNoAuditEntry(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}

	svc := user.NewService(store, user.WithAuditStore(au))
	err := svc.ResetMFA(context.Background(), uuid.New(), nil)

	require.Error(t, err, "clearing MFA on an account that does not exist must fail")
	require.Empty(t, au.entries, "a failed clear must not be recorded as having happened")
}

func TestAdminSetPassword_RecordsWhoDidIt(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	target := seedActiveUser(store)
	actorID := uuid.New()

	svc := user.NewService(store, user.WithAuditStore(au))
	require.NoError(t, svc.AdminSetPassword(context.Background(), target.ID, "a-new-passphrase", &actorID))

	require.Len(t, au.entries, 1)
	e := au.entries[0]
	require.Equal(t, "user", e.EntityType)
	require.Equal(t, target.ID, e.EntityID)
	require.Equal(t, "password_reset_by_admin", e.Action)
	require.NotNil(t, e.ActorID)
	require.Equal(t, actorID, *e.ActorID)
}

func TestAdminSetPassword_RefusalWritesNoAuditEntry(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{name: "too short", password: "short"},
		{name: "too long", password: strings.Repeat("a", 73)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeUserStore()
			au := &fakeAuditStore{}
			target := seedActiveUser(store)
			actorID := uuid.New()

			svc := user.NewService(store, user.WithAuditStore(au))
			err := svc.AdminSetPassword(context.Background(), target.ID, tc.password, &actorID)

			require.Error(t, err, "an invalid password must still be refused")
			require.Empty(t, au.entries, "a refused change is not a change; nothing happened to record")
		})
	}
}

// Same contract as TestResetMFA_StoreFailureWritesNoAuditEntry, for the
// other store call. See that test's comment for why this matters: the real
// store used to report success for a nonexistent target.
func TestAdminSetPassword_StoreFailureWritesNoAuditEntry(t *testing.T) {
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	actorID := uuid.New()

	svc := user.NewService(store, user.WithAuditStore(au))
	err := svc.AdminSetPassword(context.Background(), uuid.New(), "a-new-passphrase", &actorID)

	require.Error(t, err, "resetting the password of an account that does not exist must fail")
	require.Empty(t, au.entries, "a failed reset must not be recorded as having happened")
}
