package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// errFakeNotFound is what the fake returns when a lookup finds no row.
//
// NOTE for whoever fixes the federated-login defects: the service layer needs a
// way to tell "no such row" apart from "the database blew up". The real stores
// already do this (userstore.ErrNotFound), but internal/domain must not import
// internal/database, so the fix is a domain-level sentinel (e.g. user.ErrNotFound)
// that the stores wrap. When that lands, point this var at it.
var errFakeNotFound = user.ErrNotFound

// fakeUserStore is an in-memory implementation of user.Store for unit tests.
type fakeUserStore struct {
	byID    map[uuid.UUID]user.User
	byEmail map[string]user.User
	bySAML  map[string]user.User
	byOIDC  map[string]user.User

	// Call counters, so tests can assert that a rejected login neither
	// created nor mutated a user record.
	creates int
	updates int

	// Injectable getter failures, to simulate a transient database error.
	errGetByOIDC  error
	errGetByEmail error
	errGetBySAML  error
}

// seed inserts a user directly, without touching the call counters.
func (f *fakeUserStore) seed(u user.User) {
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	if u.SAMLSubject != "" {
		f.bySAML[u.SAMLSubject] = u
	}
	if u.OIDCSubject != "" {
		f.byOIDC[u.OIDCSubject] = u
	}
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{
		byID:    make(map[uuid.UUID]user.User),
		byEmail: make(map[string]user.User),
		bySAML:  make(map[string]user.User),
		byOIDC:  make(map[string]user.User),
	}
}

func (f *fakeUserStore) Create(_ context.Context, u user.User) error {
	f.creates++
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	if u.SAMLSubject != "" {
		f.bySAML[u.SAMLSubject] = u
	}
	if u.OIDCSubject != "" {
		f.byOIDC[u.OIDCSubject] = u
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
	if f.errGetByEmail != nil {
		return user.User{}, f.errGetByEmail
	}
	u, ok := f.byEmail[email]
	if !ok {
		return user.User{}, errFakeNotFound
	}
	return u, nil
}

func (f *fakeUserStore) GetBySAMLSubject(_ context.Context, subject string) (user.User, error) {
	if f.errGetBySAML != nil {
		return user.User{}, f.errGetBySAML
	}
	u, ok := f.bySAML[subject]
	if !ok {
		return user.User{}, errFakeNotFound
	}
	return u, nil
}

func (f *fakeUserStore) GetByOIDCSubject(_ context.Context, subject string) (user.User, error) {
	if f.errGetByOIDC != nil {
		return user.User{}, f.errGetByOIDC
	}
	u, ok := f.byOIDC[subject]
	if !ok {
		return user.User{}, errFakeNotFound
	}
	return u, nil
}

func (f *fakeUserStore) Update(_ context.Context, u user.User) error {
	f.updates++
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	if u.SAMLSubject != "" {
		f.bySAML[u.SAMLSubject] = u
	}
	if u.OIDCSubject != "" {
		f.byOIDC[u.OIDCSubject] = u
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

func (f *fakeUserStore) GetByIDAdmin(_ context.Context, id uuid.UUID) (user.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return user.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeUserStore) Restore(_ context.Context, id uuid.UUID) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.DeletedAt = nil
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) Disable(_ context.Context, id uuid.UUID) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.Disabled = true
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) Enable(_ context.Context, id uuid.UUID) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.Disabled = false
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) ListAdmin(_ context.Context, _, _ int) ([]user.User, error) {
	out := make([]user.User, 0, len(f.byID))
	for _, u := range f.byID {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeUserStore) Count(_ context.Context) (int64, error) {
	return int64(len(f.byID)), nil
}

func (f *fakeUserStore) ClearMFA(_ context.Context, id uuid.UUID) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.MFAEnabled = false
	u.MFASecret = ""
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) AdminSetPassword(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.PasswordHash = hash
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
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

	secret, qrURL, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost", false)
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

	secret, _, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost", false)
	require.NoError(t, err)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	require.NoError(t, svc.ConfirmMFAEnrollment(context.Background(), u.ID, code))

	got, err := svc.GetByID(context.Background(), u.ID)
	require.NoError(t, err)
	require.True(t, got.MFAEnabled)
}

// TestNewService_DefaultsToDefaultCost is the guard on WithBcryptCost.
//
// The option exists so the test suite can hash cheaply; the danger is that it
// leaks into production wiring, or that someone "simplifies" the default. Both
// would silently weaken every password in the database — nothing else in the
// system would notice, and no test would fail. This one does.
func TestNewService_DefaultsToDefaultCost(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store) // no options: exactly how cmd/server builds it

	created, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "cost@example.com",
		DisplayName: "Cost Check",
		Role:        user.RoleUser,
		Password:    "a-real-password",
	})
	require.NoError(t, err)

	stored := store.byID[created.ID]
	require.NotEmpty(t, stored.PasswordHash, "a password user must have a hash")

	cost, err := bcrypt.Cost([]byte(stored.PasswordHash))
	require.NoError(t, err)
	require.Equal(t, bcrypt.DefaultCost, cost,
		"the default construction must hash at bcrypt.DefaultCost; lowering it weakens every stored password")
}

// TestWithBcryptCost_FloorsAtMinCost keeps the option from producing hashes
// bcrypt itself rejects.
func TestWithBcryptCost_FloorsAtMinCost(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store, user.WithBcryptCost(1)) // below bcrypt.MinCost

	created, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "floor@example.com",
		DisplayName: "Floor Check",
		Role:        user.RoleUser,
		Password:    "a-real-password",
	})
	require.NoError(t, err)

	cost, err := bcrypt.Cost([]byte(store.byID[created.ID].PasswordHash))
	require.NoError(t, err)
	require.Equal(t, bcrypt.MinCost, cost)
}

// TestUserService_EnrollMFA_RefusesSilentReEnrolment pins the guard on a full
// MFA bypass.
//
// Enrolment overwrites the stored TOTP secret and returns the new one, while
// MFAEnabled stays true. Without this guard an attacker holding only the
// victim's PASSWORD could log in with an MFA-unverified session, re-enrol, and
// then satisfy the challenge with a code of their own — verified end to end
// against the HTTP surface before the fix. See
// internal/server/mfa_bypass_exploit_test.go for that half.
func TestUserService_EnrollMFA_RefusesSilentReEnrolment(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "heidi@example.com",
		DisplayName: "Heidi",
		Role:        user.RoleUser,
		Password:    "pass",
	})
	require.NoError(t, err)

	// First enrolment needs no prior challenge — the account is not protected yet.
	secret, _, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost", false)
	require.NoError(t, err)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmMFAEnrollment(context.Background(), u.ID, code))

	// Now protected: re-enrolment without proof of possession must be refused.
	_, _, err = svc.EnrollMFA(context.Background(), u.ID, "http://localhost", false)
	require.ErrorIs(t, err, user.ErrMFAAlreadyEnrolled)

	// And it must not have rotated the secret on the way out — a refusal that
	// still overwrote the secret would lock the legitimate user out, which is
	// most of the damage the attack does.
	after, err := svc.GetByID(context.Background(), u.ID)
	require.NoError(t, err)
	require.Equal(t, secret, after.MFASecret, "a refused re-enrolment must not rotate the secret")
	require.True(t, after.MFAEnabled)

	// Rotation stays self-service for someone who still holds the current
	// authenticator: the caller vouches for that with allowReenroll.
	rotated, _, err := svc.EnrollMFA(context.Background(), u.ID, "http://localhost", true)
	require.NoError(t, err)
	require.NotEqual(t, secret, rotated)
}
