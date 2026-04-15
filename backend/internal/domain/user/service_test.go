package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// fakeUserStore is an in-memory implementation of user.Store for unit tests.
type fakeUserStore struct {
	byID    map[uuid.UUID]user.User
	byEmail map[string]user.User
	bySAML  map[string]user.User
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{
		byID:    make(map[uuid.UUID]user.User),
		byEmail: make(map[string]user.User),
		bySAML:  make(map[string]user.User),
	}
}

func (f *fakeUserStore) Create(_ context.Context, u user.User) error {
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	if u.SAMLSubject != "" {
		f.bySAML[u.SAMLSubject] = u
	}
	return nil
}

func (f *fakeUserStore) GetByID(_ context.Context, id uuid.UUID) (user.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return user.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeUserStore) GetByEmail(_ context.Context, email string) (user.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return user.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeUserStore) GetBySAMLSubject(_ context.Context, subject string) (user.User, error) {
	u, ok := f.bySAML[subject]
	if !ok {
		return user.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeUserStore) Update(_ context.Context, u user.User) error {
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	if u.SAMLSubject != "" {
		f.bySAML[u.SAMLSubject] = u
	}
	return nil
}

func (f *fakeUserStore) SoftDelete(_ context.Context, id uuid.UUID) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	now := time.Now()
	u.DeletedAt = &now
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) List(_ context.Context, _, _ int) ([]user.User, error) {
	out := make([]user.User, 0, len(f.byID))
	for _, u := range f.byID {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeUserStore) Count(_ context.Context) (int64, error) {
	return int64(len(f.byID)), nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestUserService_Create_Valid(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "Alice@Example.COM",
		DisplayName: "Alice",
		Role:        user.RoleUser,
		Password:    "secret123",
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, u.ID)
	require.Equal(t, "alice@example.com", u.Email) // normalized to lowercase
	require.NotEmpty(t, u.PasswordHash)
	require.NotEqual(t, "secret123", u.PasswordHash) // must be hashed
}

func TestUserService_Create_MissingEmail(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	_, err := svc.Create(context.Background(), user.CreateUserInput{
		DisplayName: "Alice",
		Role:        user.RoleUser,
	})
	require.Error(t, err)
}

func TestUserService_VerifyPassword_Valid(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	_, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "bob@example.com",
		DisplayName: "Bob",
		Role:        user.RoleStaff,
		Password:    "correcthorse",
	})
	require.NoError(t, err)

	got, err := svc.VerifyPassword(context.Background(), "bob@example.com", "correcthorse")
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", got.Email)
}

func TestUserService_VerifyPassword_WrongPassword(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	_, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "carol@example.com",
		DisplayName: "Carol",
		Role:        user.RoleUser,
		Password:    "rightpass",
	})
	require.NoError(t, err)

	_, err = svc.VerifyPassword(context.Background(), "carol@example.com", "wrongpass")
	require.Error(t, err)
}

func TestUserService_VerifyPassword_InactiveUser(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "dave@example.com",
		DisplayName: "Dave",
		Role:        user.RoleUser,
		Password:    "pass",
	})
	require.NoError(t, err)
	require.NoError(t, svc.SoftDelete(context.Background(), u.ID))

	_, err = svc.VerifyPassword(context.Background(), "dave@example.com", "pass")
	require.Error(t, err)
}

func TestUserService_SetPassword(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "eve@example.com",
		DisplayName: "Eve",
		Role:        user.RoleUser,
		Password:    "oldpass",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetPassword(context.Background(), u.ID, "newpass"))

	_, err = svc.VerifyPassword(context.Background(), "eve@example.com", "oldpass")
	require.Error(t, err, "old password should no longer work")

	_, err = svc.VerifyPassword(context.Background(), "eve@example.com", "newpass")
	require.NoError(t, err, "new password should work")
}

func TestUserService_EnrollMFA(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "frank@example.com",
		DisplayName: "Frank",
		Role:        user.RoleUser,
		Password:    "pass",
	})
	require.NoError(t, err)

	secret, qrURL, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost")
	require.NoError(t, err)
	require.NotEmpty(t, secret)
	require.NotEmpty(t, qrURL)
}

func TestUserService_ConfirmMFAEnrollment(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "grace@example.com",
		DisplayName: "Grace",
		Role:        user.RoleUser,
		Password:    "pass",
	})
	require.NoError(t, err)

	secret, _, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost")
	require.NoError(t, err)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	require.NoError(t, svc.ConfirmMFAEnrollment(context.Background(), u.ID, code))

	got, err := svc.GetByID(context.Background(), u.ID)
	require.NoError(t, err)
	require.True(t, got.MFAEnabled)
}
