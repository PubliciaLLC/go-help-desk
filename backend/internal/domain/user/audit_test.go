package user_test

import (
	"context"
	"errors"
	"testing"

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
	store := newFakeUserStore()
	au := &fakeAuditStore{}
	target := seedActiveUser(store)
	actorID := uuid.New()

	svc := user.NewService(store, user.WithAuditStore(au))
	err := svc.AdminSetPassword(context.Background(), target.ID, "short", &actorID)

	require.Error(t, err, "below MinPasswordLength must still be refused")
	require.Empty(t, au.entries, "a refused change is not a change; nothing happened to record")
}
