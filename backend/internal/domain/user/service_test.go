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
	mfaFailures map[uuid.UUID]int
	mfaLocks    map[uuid.UUID]*time.Time
	byID        map[uuid.UUID]user.User
	byEmail     map[string]user.User
	bySAML      map[string]user.User
	byOIDC      map[string]user.User

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
		byID:        make(map[uuid.UUID]user.User),
		byEmail:     make(map[string]user.User),
		bySAML:      make(map[string]user.User),
		byOIDC:      make(map[string]user.User),
		mfaFailures: make(map[uuid.UUID]int),
		mfaLocks:    make(map[uuid.UUID]*time.Time),
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

func (f *fakeUserStore) ListActiveAdmins(_ context.Context) ([]user.User, error) {
	out := make([]user.User, 0, len(f.byID))
	for _, u := range f.byID {
		if u.Role == user.RoleAdmin && u.IsActive() {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeUserStore) Count(_ context.Context) (int64, error) {
	return int64(len(f.byID)), nil
}

// EmailIsTaken covers deleted rows, because the real unique constraint does.
func (f *fakeUserStore) EmailIsTaken(_ context.Context, email string) (bool, error) {
	_, ok := f.byEmail[email]
	return ok, nil
}

// UpdateProfile writes only the address and the name, leaving everything else
// on the row alone — which is the whole point of it existing.
func (f *fakeUserStore) UpdateProfile(_ context.Context, id uuid.UUID, email, displayName string) error {
	u, ok := f.byID[id]
	if !ok {
		return errFakeNotFound
	}
	delete(f.byEmail, u.Email)
	u.Email = email
	u.DisplayName = displayName
	u.UpdatedAt = time.Now()
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

// The narrow writes. Each touches the one thing it names and leaves the rest
// of the row alone — which is the property the whole-row Update did not have.
func (f *fakeUserStore) SetPasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := f.byID[id]
	if !ok {
		return errFakeNotFound
	}
	u.PasswordHash = hash
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) SetMFA(_ context.Context, id uuid.UUID, secret string, enabled bool) error {
	u, ok := f.byID[id]
	if !ok {
		return errFakeNotFound
	}
	u.MFASecret, u.MFAEnabled = secret, enabled
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return nil
}

func (f *fakeUserStore) SetFirstMFA(_ context.Context, id uuid.UUID, secret string) (bool, error) {
	u, ok := f.byID[id]
	if !ok {
		return false, errFakeNotFound
	}
	if u.MFAEnabled {
		return false, nil
	}
	u.MFASecret, u.MFAEnabled = secret, true
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return true, nil
}

// SyncFederated counts as an update, because that is what the OIDC and SAML
// tests are asking about: whether the sign-in wrote the profile back. It goes
// through the narrow statement now rather than the whole-row one, which is
// the point of it existing — a sign-in must not carry a role or a password
// hash along with the name.
func (f *fakeUserStore) SyncFederated(ctx context.Context, id uuid.UUID, email, displayName string) error {
	f.updates++
	return f.UpdateProfile(ctx, id, email, displayName)
}

// EnableMFAIfStillEnrolled mirrors the statement: the flag only, and only
// while a secret is still there. A fake that wrote the secret back would hide
// the very thing this exists to stop.
func (f *fakeUserStore) EnableMFAIfStillEnrolled(_ context.Context, id uuid.UUID) (bool, error) {
	u, ok := f.byID[id]
	if !ok || u.MFASecret == "" || u.DeletedAt != nil {
		return false, nil
	}
	u.MFAEnabled = true
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return true, nil
}

// AdoptOIDCSubject applies the same conditions the statement does, from the
// stored row rather than from whatever the caller read earlier — which is the
// whole reason the real one is a single statement.
func (f *fakeUserStore) AdoptOIDCSubject(_ context.Context, id uuid.UUID, subject, displayName string) (bool, error) {
	u, ok := f.byID[id]
	if !ok {
		return false, nil
	}
	if u.Disabled || u.DeletedAt != nil || u.Role == user.RoleAdmin ||
		u.SAMLSubject != "" || (u.OIDCSubject != "" && u.OIDCSubject != subject) {
		return false, nil
	}
	f.updates++
	u.OIDCSubject = subject
	if displayName != "" {
		u.DisplayName = displayName
	}
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	f.byOIDC[subject] = u
	return true, nil
}

// The three guarded writes. This fake applies the same rule the SQL does:
// refuse when the change would leave no active administrator.
func (f *fakeUserStore) lastActiveAdmin(id uuid.UUID) bool {
	for otherID, u := range f.byID {
		if otherID == id || u.Role != user.RoleAdmin || u.Disabled || u.DeletedAt != nil {
			continue
		}
		return false
	}
	return true
}

func (f *fakeUserStore) DisableUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	if f.byID[id].Role == user.RoleAdmin && f.lastActiveAdmin(id) {
		return false, nil
	}
	return true, f.Disable(ctx, id)
}

func (f *fakeUserStore) SoftDeleteUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	if f.byID[id].Role == user.RoleAdmin && f.lastActiveAdmin(id) {
		return false, nil
	}
	return true, f.SoftDelete(ctx, id)
}

func (f *fakeUserStore) SetRoleUnlessLastAdmin(_ context.Context, id uuid.UUID, role string) (bool, error) {
	if role != string(user.RoleAdmin) && f.byID[id].Role == user.RoleAdmin && f.lastActiveAdmin(id) {
		return false, nil
	}
	u := f.byID[id]
	u.Role = user.Role(role)
	f.byID[id] = u
	f.byEmail[u.Email] = u
	return true, nil
}

// ListAssignableStaff returns the active staff and admins in this fake.
func (f *fakeUserStore) ListAssignableStaff(_ context.Context) ([]user.AssignableStaff, error) {
	var out []user.AssignableStaff
	for _, u := range f.byID {
		if u.DeletedAt != nil {
			continue
		}
		out = append(out, user.AssignableStaff{
			ID: u.ID, DisplayName: u.DisplayName,
			Assignable: u.Role == user.RoleStaff || u.Role == user.RoleAdmin,
		})
	}
	return out, nil
}

// CountOtherActiveAdmins counts the administrators left if this one stopped
// being one.
func (f *fakeUserStore) CountOtherActiveAdmins(_ context.Context, excluding uuid.UUID) (int64, error) {
	var n int64
	for id, u := range f.byID {
		if id == excluding || u.Role != user.RoleAdmin || u.Disabled || u.DeletedAt != nil {
			continue
		}
		n++
	}
	return n, nil
}

// CountAll counts every row. This fake never removes one, so it is the same
// number — which is the point: the real store's two counts differ, and that
// difference is what reopened the setup route.
func (f *fakeUserStore) CountAll(_ context.Context) (int64, error) {
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
		Password:    "a-passphrase",
	})
	require.NoError(t, err)
	require.NoError(t, svc.SoftDelete(context.Background(), u.ID))

	_, err = svc.VerifyPassword(context.Background(), "dave@example.com", "a-passphrase")
	require.Error(t, err)
}

func TestUserService_SetPassword(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "eve@example.com",
		DisplayName: "Eve",
		Role:        user.RoleUser,
		Password:    "old-passphrase",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetPassword(context.Background(), u.ID, "new-passphrase"))

	_, err = svc.VerifyPassword(context.Background(), "eve@example.com", "old-passphrase")
	require.Error(t, err, "old password should no longer work")

	_, err = svc.VerifyPassword(context.Background(), "eve@example.com", "new-passphrase")
	require.NoError(t, err, "new password should work")
}

func TestUserService_EnrollMFA(t *testing.T) {
	svc := user.NewService(newFakeUserStore())
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email:       "frank@example.com",
		DisplayName: "Frank",
		Role:        user.RoleUser,
		Password:    "a-passphrase",
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
		Password:    "a-passphrase",
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
		Password:    "a-passphrase",
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

// VerifyPassword used to return the moment the email lookup missed, without
// hashing anything. A nonexistent address therefore answered in microseconds
// while a real one paid the full bcrypt cost — a reliable account-enumeration
// oracle no matter how identical the status code and body are kept.
//
// Asserted as a ratio rather than an absolute, because absolute timings are
// machine- and load-dependent. The bound is deliberately loose: before the fix
// the miss path was orders of magnitude faster, so anything near parity proves
// the work is being spent.
func TestVerifyPassword_MissCostsTheSameAsAHit(t *testing.T) {
	svc := user.NewService(newFakeUserStore(), user.WithBcryptCost(bcrypt.DefaultCost))
	u, err := svc.Create(context.Background(), user.CreateUserInput{
		Email: "real@example.com", DisplayName: "Real", Role: user.RoleUser, Password: "correct horse",
	})
	require.NoError(t, err)
	require.NotEmpty(t, u.ID)

	measure := func(email string) time.Duration {
		start := time.Now()
		_, _ = svc.VerifyPassword(context.Background(), email, "some guess")
		return time.Since(start)
	}

	// Warm up, so the first bcrypt call's setup does not skew the comparison.
	measure("real@example.com")

	hit := measure("real@example.com")
	miss := measure("does-not-exist@example.com")

	require.Greater(t, miss*4, hit,
		"a miss (%v) must not be dramatically faster than a hit (%v) — that is an enumeration oracle", miss, hit)
}

// MFA attempt tracking. The fake keeps it in the struct so a test can assert
// that failures are counted and cleared.
// ClaimMFAAttempt mirrors the real statement: an expired lock resets the
// window, a live one holds its deadline while the count keeps rising, and
// otherwise the attempt is counted and the lock set once the budget is spent.
func (f *fakeUserStore) ClaimMFAAttempt(_ context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (int, *time.Time, error) {
	if until := f.mfaLocks[id]; until != nil {
		if time.Now().After(*until) {
			f.mfaFailures[id] = 1
			f.mfaLocks[id] = nil
			return 1, nil, nil
		}
		f.mfaFailures[id]++
		return f.mfaFailures[id], until, nil
	}
	f.mfaFailures[id]++
	if f.mfaFailures[id] >= maxAttempts {
		lockedUntil := time.Now().Add(lockFor)
		f.mfaLocks[id] = &lockedUntil
	}
	return f.mfaFailures[id], f.mfaLocks[id], nil
}

func (f *fakeUserStore) RecordMFAFailure(_ context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (int, *time.Time, error) {
	f.mfaFailures[id]++
	if f.mfaFailures[id] >= maxAttempts {
		until := time.Now().Add(lockFor)
		f.mfaLocks[id] = &until
	}
	return f.mfaFailures[id], f.mfaLocks[id], nil
}

func (f *fakeUserStore) ClearMFAFailures(_ context.Context, id uuid.UUID) error {
	delete(f.mfaFailures, id)
	delete(f.mfaLocks, id)
	return nil
}

func (f *fakeUserStore) GetMFALock(_ context.Context, id uuid.UUID) (int, *time.Time, error) {
	return f.mfaFailures[id], f.mfaLocks[id], nil
}
