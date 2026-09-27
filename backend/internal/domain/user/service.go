package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Service orchestrates user-related business operations.
type Service struct {
	store Store

	// hashCost is the bcrypt cost for password hashing. Always
	// bcrypt.DefaultCost in production; see WithBcryptCost.
	hashCost int

	// dummyHash makes a miss cost the same as a hit; see compareAgainstDummyHash.
	dummyHash []byte
}

// NewService returns a Service backed by the given Store.
func NewService(store Store, opts ...Option) *Service {
	s := &Service{store: store, hashCost: bcrypt.DefaultCost}
	for _, opt := range opts {
		opt(s)
	}
	// Computed once, at the configured cost, so a failed lookup can spend the
	// same time a real comparison would. Generated rather than hard-coded
	// because the cost is configurable and a constant from another cost would
	// time differently, which is the whole thing being avoided.
	if h, err := bcrypt.GenerateFromPassword([]byte("dummy password for constant-time misses"), s.hashCost); err == nil {
		s.dummyHash = h
	}
	return s
}

// compareAgainstDummyHash burns the same work a real password check would.
//
// Without it, VerifyPassword returns in microseconds for an address with no
// account and takes the full bcrypt cost for one that has an account — a
// reliable enumeration oracle no matter how carefully the response body and
// status code are kept identical.
func (s *Service) compareAgainstDummyHash(plain string) {
	if len(s.dummyHash) == 0 {
		return
	}
	_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(plain))
}

// Option configures a Service at construction.
type Option func(*Service)

// WithBcryptCost overrides the bcrypt cost used when hashing passwords.
//
// THIS EXISTS FOR TESTS AND MUST NOT BE USED IN PRODUCTION WIRING. The cost is
// a security parameter: it is what makes an offline attack on a stolen password
// hash expensive, and lowering it weakens every password in the database.
//
// In a test suite it is pure latency and nothing else. The server integration
// tests build ~80 harnesses, each creating several users, and under -race a
// single hash at the default cost takes about a second: the suite spent 140 of
// its 152 seconds hashing passwords nobody asserts anything about.
//
// Costs below bcrypt.MinCost are raised to it, since bcrypt rejects them.
// cmd/server never calls this, and TestNewService_DefaultsToDefaultCost fails
// if the default ever changes.
func WithBcryptCost(cost int) Option {
	return func(s *Service) {
		if cost < bcrypt.MinCost {
			cost = bcrypt.MinCost
		}
		s.hashCost = cost
	}
}

// CreateUserInput is the data needed to create a new user.
type CreateUserInput struct {
	Email        string
	DisplayName  string
	Role         Role
	Password     string // plain text; hashed by Create; empty if SAML-only or pre-hashed
	PasswordHash string // pre-computed bcrypt hash; used only when Password is empty
	SAMLSubject  string // empty if local-only
	OIDCSubject  string // empty if not OIDC
}

// Create validates and persists a new user, hashing the password if provided.
func (s *Service) Create(ctx context.Context, in CreateUserInput) (User, error) {
	u := User{
		ID:          uuid.New(),
		Email:       strings.ToLower(strings.TrimSpace(in.Email)),
		DisplayName: strings.TrimSpace(in.DisplayName),
		Role:        in.Role,
		SAMLSubject: in.SAMLSubject,
		OIDCSubject: in.OIDCSubject,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := u.Validate(); err != nil {
		return User{}, err
	}
	switch {
	case in.Password != "":
		// The same minimum every other path applies. Admin create and
		// first-run setup accepted one character; a one-character password on
		// an administrator account created during setup is the worst case of
		// the four and was the least guarded.
		if len(in.Password) < MinPasswordLength {
			return User{}, fmt.Errorf("%w: password must be at least %d characters",
				ErrValidation, MinPasswordLength)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), s.hashCost)
		if err != nil {
			return User{}, fmt.Errorf("hashing password: %w", err)
		}
		u.PasswordHash = string(hash)
	case in.PasswordHash != "":
		u.PasswordHash = in.PasswordHash
	}
	if err := s.store.Create(ctx, u); err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

// SetPassword hashes and stores a new password for the given user.
func (s *Service) SetPassword(ctx context.Context, userID uuid.UUID, plain string) error {
	// The fourth path, held to the same minimum as the other three. The
	// handler checks it too; this is the check that cannot be bypassed by a
	// future caller that forgets.
	if len(plain) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters",
			ErrValidation, MinPasswordLength)
	}
	if _, err := s.store.GetByID(ctx, userID); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), s.hashCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	// Only the hash. This used to write every column back from the copy read
	// above, with a bcrypt hash in between — 45 to 66 milliseconds at the
	// production cost, and the account holder chooses the moment. So demoting
	// a compromised account while its owner was changing their password wrote
	// `admin` back over the demotion, and they signed in again with the new
	// password as an administrator. The same window undid an administrator's
	// MFA reset and reverted a corrected address.
	return s.store.SetPasswordHash(ctx, userID, string(hash))
}

// ErrLastAdmin is the refusal to remove the only administrator.
//
// Disabling, demoting or deleting a sole admin account all answered success,
// and the next request answered 401. Setup does not reopen — that is
// deliberate, and it is what makes this unrecoverable: the instance is left
// with no way in short of editing the database by hand.
var ErrLastAdmin = fmt.Errorf("%w: this is the only administrator, so it cannot be disabled, demoted or deleted", ErrValidation)

// Disable, SoftDelete and SetRole each refuse to remove the last active
// administrator, and each decides that in the same statement that writes.
//
// The first version asked a separate question first — count the other
// administrators, then write if there were any — and that lost the race it
// was written for. Measured against a live database: two administrators
// demoting each other, twenty-eight rounds in thirty ended with none. It did
// not even need two people; one administrator sending "remove Bob" and
// "remove me" at the same moment did it in all thirty. The window is the gap
// between the two statements, and the only way to close it is not to have
// one.

// refusalFor turns a guarded write that did not apply into the reason it did
// not.
//
// The three ...UnlessLastAdmin statements report one bit: applied, or not.
// Not-applied has two causes — the guard refused because this is the last
// administrator, or there was no live row to act on at all — and reporting
// both as ErrLastAdmin told an administrator "this is the only
// administrator" about an account that had been deleted, which is a lie
// about a different thing. It did that before these statements filtered
// their own target row, too: an id that matched nothing already came back
// that way.
//
// Read on the failure path only. The write has already been refused, so this
// is choosing a message rather than deciding an outcome, and a row deleted
// between the two simply gets the other true answer.
func (s *Service) refusalFor(ctx context.Context, id uuid.UUID) error {
	u, err := s.store.GetByIDAdmin(ctx, id)
	if err != nil {
		return err
	}
	if u.DeletedAt != nil {
		return ErrNotFound
	}
	return ErrLastAdmin
}

func (s *Service) SetRole(ctx context.Context, id uuid.UUID, role Role) error {
	switch role {
	case RoleAdmin, RoleStaff, RoleUser:
	default:
		return fmt.Errorf("%w: invalid role", ErrValidation)
	}
	applied, err := s.store.SetRoleUnlessLastAdmin(ctx, id, string(role))
	if err != nil {
		return err
	}
	if !applied {
		return s.refusalFor(ctx, id)
	}
	return nil
}

// EmailIsTaken reports whether an address already belongs to an account here,
// deleted accounts included.
//
// The deleted ones are the point. GetByEmail hides them, which is right for
// logging in and wrong for "can this be registered": the unique constraint is
// on every row, so a deleted account still owns its address. Checking with
// the login lookup let a signup through, sent a verification email, and then
// failed at the end with a message about the token.
func (s *Service) EmailIsTaken(ctx context.Context, email string) (bool, error) {
	addr, err := ValidateEmail(email)
	if err != nil {
		return false, err
	}
	return s.store.EmailIsTaken(ctx, addr)
}

// UpdateProfile changes an account's address and name, and nothing else.
//
// Update writes the whole row from a struct the caller read earlier, so
// anything that changed in between is written back: a password set moments
// ago stops working, an MFA enrolment is undone, another administrator's role
// change is reverted. A rename should rename.
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, email, displayName string) error {
	addr, err := ValidateEmail(email)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		return fmt.Errorf("%w: display name is required", ErrValidation)
	}
	return s.store.UpdateProfile(ctx, id, addr, name)
}

// ListAssignableStaff returns the people work can be given to.
func (s *Service) ListAssignableStaff(ctx context.Context) ([]AssignableStaff, error) {
	return s.store.ListAssignableStaff(ctx)
}

// GetByEmail returns the account holding an address.
//
// Exported for the signup flow, which needs to know whether an address is
// already taken BEFORE it sends a verification email — otherwise the person
// follows a link that cannot work and is told their token is invalid. The
// answer is never shown to whoever is signing up; see
// registration.ErrAlreadyRegistered.
func (s *Service) GetByEmail(ctx context.Context, email string) (User, error) {
	addr, err := ValidateEmail(email)
	if err != nil {
		return User{}, err
	}
	return s.store.GetByEmail(ctx, addr)
}

// VerifyPassword looks up a user by email and checks the plain-text password.
func (s *Service) VerifyPassword(ctx context.Context, email, plain string) (User, error) {
	u, err := s.store.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		// Spend the same work on a miss as on a hit. Returning here without
		// hashing made a nonexistent address answer in microseconds while a
		// real one took the full bcrypt cost, which is a reliable account
		// enumeration oracle regardless of what the response body says.
		s.compareAgainstDummyHash(plain)
		return User{}, fmt.Errorf("looking up user: %w", err)
	}
	if !u.IsActive() {
		s.compareAgainstDummyHash(plain)
		return User{}, fmt.Errorf("user account is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plain)); err != nil {
		return User{}, fmt.Errorf("invalid credentials")
	}
	return u, nil
}

// EnrollMFA generates a TOTP secret for the user, stores it (unenrolled until
// confirmed), and returns the secret and a data URL for a QR code.
// ErrMFAAlreadyEnrolled is returned when a caller tries to re-enrol an account
// that already has MFA active, without having proved they still control the
// current authenticator.
//
// This is the guard on a full MFA bypass. Enrolment overwrites the stored TOTP
// secret and hands the new one back, while MFAEnabled stays true — so an
// attacker holding only the victim's PASSWORD could log in with an
// MFA-unverified session, re-enrol, and verify with a code of their own
// making. The enrolment route is deliberately outside RequireMFA so a user
// compelled to enrol can finish; that is correct, and this is where the
// difference between "has not enrolled yet" and "is already protected" has to
// be enforced.
var ErrMFAAlreadyEnrolled = errors.New("MFA is already enabled for this account")

// EnrollMFA generates a new TOTP secret for a user and stores it.
//
// allowReenroll must be true to re-enrol an account that already has MFA
// active, and the caller may only pass true once it has established that the
// request comes from someone who already satisfied the existing MFA challenge.
// It is a parameter rather than something derived here because internal/domain
// has no access to the request's session state.
//
// Rotating to a new authenticator while still holding the old one therefore
// stays self-service; an account whose authenticator is lost needs an
// administrator reset, which is the only safe answer — a user who cannot
// produce a current code is indistinguishable from an attacker who never had
// one.
// ConfirmMFAEnrollmentWith validates a code against a secret the caller staged
// and, only on success, makes that secret the user's.
//
// The secret arrives from the caller's session rather than the user row,
// because writing an unconfirmed secret to the row destroys the authenticator
// the user is still using.
func (s *Service) ConfirmMFAEnrollmentWith(ctx context.Context, userID uuid.UUID, pendingSecret, code string) error {
	if pendingSecret == "" {
		return fmt.Errorf("MFA enrollment not started")
	}
	if !totp.Validate(code, pendingSecret) {
		return fmt.Errorf("invalid TOTP code")
	}
	if _, err := s.store.GetByID(ctx, userID); err != nil {
		return err
	}
	// Only the secret and the flag: the same reason as SetPassword. This read
	// the row, validated a code, and wrote everything back.
	return s.store.SetMFA(ctx, userID, pendingSecret, true)
}

// GenerateMFASecret mints a secret and its otpauth URL WITHOUT persisting
// anything. The caller stages it until the user proves possession.
func (s *Service) GenerateMFASecret(ctx context.Context, userID uuid.UUID, issuer string, allowReenroll bool) (secret, qrURL string, err error) {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.MFAEnabled && !allowReenroll {
		return "", "", ErrMFAAlreadyEnrolled
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: u.Email})
	if err != nil {
		return "", "", fmt.Errorf("generating TOTP key: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// Deprecated: EnrollMFA writes an unconfirmed secret to the user row, which
// destroys the authenticator the user is still relying on. Use
// GenerateMFASecret to mint one and ConfirmMFAEnrollmentWith to adopt it after
// the user proves possession. No production caller remains; kept only because
// tests still exercise it, and removal belongs in its own commit.
func (s *Service) EnrollMFA(ctx context.Context, userID uuid.UUID, issuer string, allowReenroll bool) (secret, qrDataURL string, err error) {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.MFAEnabled && !allowReenroll {
		return "", "", ErrMFAAlreadyEnrolled
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: u.Email,
	})
	if err != nil {
		return "", "", fmt.Errorf("generating TOTP key: %w", err)
	}
	u.MFASecret = key.Secret()
	// MFAEnabled stays false until the user confirms with a valid code.
	u.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, u); err != nil {
		return "", "", fmt.Errorf("saving MFA secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ConfirmMFAEnrollment enables MFA for the user after they verify a TOTP code.
func (s *Service) ConfirmMFAEnrollment(ctx context.Context, userID uuid.UUID, code string) error {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.MFASecret == "" {
		return fmt.Errorf("MFA enrollment not started")
	}
	if !totp.Validate(code, u.MFASecret) {
		return fmt.Errorf("invalid TOTP code")
	}
	// The flag alone, against the secret the row holds NOW — not the copy
	// read above.
	//
	// Validating a TOTP code takes time, and an administrator's "reset MFA"
	// committing in that window used to be undone: the secret from the read
	// was written back with the flag, so the cleared authenticator came back
	// and MFA was re-enabled with exactly the credential the reset existed to
	// revoke. The statement refuses when no secret is left, so the reset
	// wins.
	applied, err := s.store.EnableMFAIfStillEnrolled(ctx, userID)
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("MFA enrollment was reset before it could be confirmed")
	}
	return nil
}

// VerifyMFACode checks that the TOTP code is valid for the user.
func (s *Service) VerifyMFACode(ctx context.Context, userID uuid.UUID, code string) error {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.MFAEnabled {
		return fmt.Errorf("MFA is not enabled")
	}
	if !totp.Validate(code, u.MFASecret) {
		return fmt.Errorf("invalid TOTP code")
	}
	return nil
}

// Federated-login refusals. They are sentinels so that the HTTP boundary can
// map them to status codes instead of reporting an internal error.
var (
	// ErrDomainNotAllowed is returned when an email's domain is not in the allowlist.
	ErrDomainNotAllowed = errors.New("email domain not allowed")

	// ErrUserDisabled is returned when the matched account may not authenticate.
	ErrUserDisabled = errors.New("user account is disabled")

	// ErrAccountLinkRefused is returned when an external identity asks to adopt
	// an existing local account that must not be handed over.
	ErrAccountLinkRefused = errors.New("refusing to link external identity to an existing account")

	// ErrSubjectRequired is returned when an external identity arrives without a
	// stable subject claim, which is malformed: the subject is the only
	// immutable key a federated account can be bound to.
	ErrSubjectRequired = errors.New("a federated subject is required")

	// ErrEmailRequired is returned when a federated identity carries no usable
	// email address, so no account can be provisioned for it.
	ErrEmailRequired = errors.New("a usable email address is required")
)

// IsEmailDomainAllowed returns true when the email domain matches one of the
// allowed domains, or when the allowed list is empty (unrestricted).
func IsEmailDomainAllowed(email string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(email)), "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	domain := parts[1]
	for _, d := range allowed {
		if strings.ToLower(strings.TrimSpace(d)) == domain {
			return true
		}
	}
	return false
}

// UpsertSAMLUser creates or updates a user record based on a SAML assertion.
// If a user with the given SAML subject already exists, their email and
// display name are updated. If not, a new user with the User role is created,
// provided the email domain is in allowedDomains (or the list is empty).

// UpsertFederatedUser creates or updates a user from an external identity provider.
//
// providerSubject is the stable identifier from the IdP:
// - SAML NameID
// - OIDC sub claim
//
// This is intentionally provider-neutral so additional identity providers
// can share the same user lifecycle.
func (s *Service) UpsertFederatedUser(
	ctx context.Context,
	providerSubject string,
	email string,
	displayName string,
	allowedDomains []string,
) (User, error) {

	return s.UpsertSAMLUser(
		ctx,
		providerSubject,
		email,
		displayName,
		allowedDomains,
	)
}

// UpsertOIDCUser creates or updates a user record from an OIDC identity.
//
// The "sub" claim is the primary key: it is immutable and scoped to the
// provider. Email is only a secondary lookup key, used to adopt an account that
// already exists locally the first time its owner logs in through the IdP — and
// only when that account is safe to adopt (see canAdoptByEmail).
//
// Callers must pass an empty email when the IdP has not verified it: an
// unverified address is not evidence of who owns it. An empty email is never
// used to look anything up, and never overwrites a stored address.
//
// The signature carries no allowed-domain list: the caller enforces that policy
// (see IsEmailDomainAllowed), because for OIDC it applies to the whole login,
// not only to just-in-time provisioning.
func (s *Service) UpsertOIDCUser(
	ctx context.Context,
	oidcSubject string,
	email string,
	displayName string,
) (User, error) {
	oidcSubject = strings.TrimSpace(oidcSubject)
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)

	// An identity with no subject cannot be bound to an account. The subject
	// lookup would miss (the query ignores empty subjects), so without this
	// guard every such login would re-run the email-adoption path and store a
	// user whose OIDCSubject is empty — leaving the "already bound to another
	// subject" check permanently disarmed for that account.
	if oidcSubject == "" {
		return User{}, ErrSubjectRequired
	}

	u, err := s.store.GetByOIDCSubject(ctx, oidcSubject)
	switch {
	case err == nil:
		// Known identity — sync the profile from the claims we were given.
		if !u.IsActive() {
			return User{}, ErrUserDisabled
		}
		if email != "" {
			u.Email = email
		}
		if displayName != "" {
			u.DisplayName = displayName
		}
		u.UpdatedAt = time.Now()
		// The address and the name, and nothing else.
		//
		// This used to write the whole row on every sign-in, carrying role,
		// password hash and MFA state from the read a few statements up. A
		// login is something the account holder triggers whenever they like,
		// so a demotion or an administrator's password reset landing in that
		// window was written back by their next sign-in — and they were an
		// administrator again, with their old password.
		//
		// ErrEmailTaken because the address the provider now sends may belong
		// to another account here. Without it the person got "an internal
		// error occurred" on every attempt and nothing told the administrator
		// which two accounts collide.
		if err := s.store.SyncFederated(ctx, u.ID, u.Email, u.DisplayName); err != nil {
			return User{}, err
		}
		return u, nil
	case !errors.Is(err, ErrNotFound):
		// A store failure is not a missing row: falling through here would
		// create a second account for an identity that already has one.
		return User{}, fmt.Errorf("looking up user by OIDC subject: %w", err)
	}

	if email != "" {
		u, err := s.store.GetByEmail(ctx, email)
		switch {
		case err == nil:
			if err := canAdoptByEmail(u, oidcSubject); err != nil {
				return User{}, err
			}
			// The subject and, if the provider sent one, the name. Nothing
			// else.
			//
			// This used to write the whole row from the read above, so an
			// administrator's password reset, disable or promotion landing
			// between the two was written back. The rules in
			// canAdoptByEmail were read from that same stale copy, so an
			// account promoted in the window was adopted as an
			// administrator — which is the one account type adoption is
			// never allowed to take. The statement asks them again at the
			// write; canAdoptByEmail stays because it is what says which
			// rule refused.
			applied, err := s.store.AdoptOIDCSubject(ctx, u.ID, oidcSubject, displayName)
			if err != nil {
				return User{}, fmt.Errorf("linking OIDC subject to user: %w", err)
			}
			if !applied {
				return User{}, fmt.Errorf("%w: the account changed while it was being linked",
					ErrAccountLinkRefused)
			}
			u.OIDCSubject = oidcSubject
			if displayName != "" {
				u.DisplayName = displayName
			}
			u.UpdatedAt = time.Now()
			return u, nil
		case !errors.Is(err, ErrNotFound):
			return User{}, fmt.Errorf("looking up user by email: %w", err)
		}
		return s.Create(ctx, CreateUserInput{
			Email:       email,
			DisplayName: displayName,
			Role:        RoleUser,
			OIDCSubject: oidcSubject,
		})
	}

	// No subject match and no address to provision from.
	return User{}, ErrEmailRequired
}

// canAdoptByEmail reports whether an unknown OIDC subject may take over the
// local account found by email address. Adopting an account hands it to
// whoever the IdP says owns that address, so it is allowed only for an active,
// unprivileged account that is not already federated.
func canAdoptByEmail(u User, oidcSubject string) error {
	switch {
	case !u.IsActive():
		return ErrUserDisabled
	case u.Role == RoleAdmin:
		return fmt.Errorf("%w: the account is an administrator", ErrAccountLinkRefused)
	case u.SAMLSubject != "":
		return fmt.Errorf("%w: the account already federates via SAML", ErrAccountLinkRefused)
	case u.OIDCSubject != "" && u.OIDCSubject != oidcSubject:
		return fmt.Errorf("%w: the account is bound to another OIDC subject", ErrAccountLinkRefused)
	}
	return nil
}

func (s *Service) UpsertSAMLUser(ctx context.Context, samlSubject, email, displayName string, allowedDomains []string) (User, error) {
	samlSubject = strings.TrimSpace(samlSubject)
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)

	// An assertion with no NameID cannot be bound to an account. The subject
	// lookup would miss — the unique index on saml_subject is partial and
	// ignores the empty sentinel every local account carries — so without this
	// guard each such login falls to the create path and mints another
	// empty-subject account.
	if samlSubject == "" {
		return User{}, ErrSubjectRequired
	}

	u, err := s.store.GetBySAMLSubject(ctx, samlSubject)
	switch {
	case err == nil:
		// Known identity — sync the profile from the attributes we were given.
		// The domain restriction deliberately does not apply to existing users,
		// matching the behaviour this path has always had.
		if !u.IsActive() {
			return User{}, ErrUserDisabled
		}
		// Synced only when present. An IdP that stops releasing an attribute
		// must not blank what is already stored: the email column is unique, so
		// the second user that happened to could not log in at all.
		if email != "" {
			u.Email = email
		}
		if displayName != "" {
			u.DisplayName = displayName
		}
		u.UpdatedAt = time.Now()
		// The address and the name, and nothing else.
		//
		// This used to write the whole row on every sign-in, carrying role,
		// password hash and MFA state from the read a few statements up. A
		// login is something the account holder triggers whenever they like,
		// so a demotion or an administrator's password reset landing in that
		// window was written back by their next sign-in — and they were an
		// administrator again, with their old password.
		//
		// ErrEmailTaken because the address the provider now sends may belong
		// to another account here. Without it the person got "an internal
		// error occurred" on every attempt and nothing told the administrator
		// which two accounts collide.
		if err := s.store.SyncFederated(ctx, u.ID, u.Email, u.DisplayName); err != nil {
			return User{}, err
		}
		return u, nil
	case !errors.Is(err, ErrNotFound):
		// A store failure is not a missing row: falling through here would
		// create a second account for an identity that already has one.
		return User{}, fmt.Errorf("looking up user by SAML subject: %w", err)
	}

	// New user — enforce domain restriction before creating.
	if !IsEmailDomainAllowed(email, allowedDomains) {
		return User{}, ErrDomainNotAllowed
	}
	return s.Create(ctx, CreateUserInput{
		Email:       email,
		DisplayName: displayName,
		Role:        RoleUser,
		SAMLSubject: samlSubject,
	})
}

// HasUsers returns true when at least one user record exists, in the sense
// that decides whether first-run setup is still open.
//
// Every row, not every usable account. Counting only live accounts made the
// setup route reopen the moment the last one was disabled or soft-deleted —
// and an administrator can do that to their own, sole, admin account, because
// nothing stops them. The route then answered {"needed": true} to anyone on
// the internet, and a POST handed them an administrator over the existing
// data: every ticket, every customer, every attachment. Proven against the
// real server.
//
// Nothing hard-deletes a user, so a row count is a durable record that this
// instance was set up once.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := s.store.CountAll(ctx)
	if err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	return n > 0, nil
}

// GetByID returns the user with the given ID.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	return s.store.GetByID(ctx, id)
}

// List returns a paginated list of users.
func (s *Service) List(ctx context.Context, limit, offset int) ([]User, error) {
	return s.store.List(ctx, limit, offset)
}

// SoftDelete marks a user as deleted without removing their data.
func (s *Service) SoftDelete(ctx context.Context, id uuid.UUID) error {
	applied, err := s.store.SoftDeleteUnlessLastAdmin(ctx, id)
	if err != nil {
		return err
	}
	if !applied {
		return s.refusalFor(ctx, id)
	}
	return nil
}

// Deprecated: Update writes every column from a copy the caller read
// earlier, so anything an administrator changed in between — a role, a
// password, an MFA enrolment — is written back. Use the narrow writers
// instead: UpdateProfile, SetPassword, SetRole, ConfirmMFAEnrollmentWith.
// No production caller remains; kept only because tests still exercise it,
// and removal belongs in its own commit.
func (s *Service) Update(ctx context.Context, u User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	u.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, u); err != nil {
		// The admin edit is the route this actually happens on — somebody
		// correcting an address and typing one another account already has.
		// It answered 500 "an internal error occurred", which is the wrong
		// thing to tell somebody about their own typo.
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return err
	}
	return nil
}

// GetByIDAdmin returns the user with the given ID, including disabled users.
func (s *Service) GetByIDAdmin(ctx context.Context, id uuid.UUID) (User, error) {
	return s.store.GetByIDAdmin(ctx, id)
}

// ListAdmin returns all users including disabled ones.
func (s *Service) ListAdmin(ctx context.Context, limit, offset int) ([]User, error) {
	return s.store.ListAdmin(ctx, limit, offset)
}

// ListActiveAdmins returns every administrator who is neither disabled nor
// soft-deleted. See StrandedAdmins.
func (s *Service) ListActiveAdmins(ctx context.Context) ([]User, error) {
	return s.store.ListActiveAdmins(ctx)
}

// Disable marks a user account as disabled without deleting it, unless it is
// the last active administrator. See SetRole for why the guard is in the
// statement rather than in front of it.
func (s *Service) Disable(ctx context.Context, id uuid.UUID) error {
	applied, err := s.store.DisableUnlessLastAdmin(ctx, id)
	if err != nil {
		return err
	}
	if !applied {
		return s.refusalFor(ctx, id)
	}
	return nil
}

// Enable re-activates a disabled user account.
func (s *Service) Enable(ctx context.Context, id uuid.UUID) error {
	return s.store.Enable(ctx, id)
}

// ResetMFA clears the user's TOTP secret and disables MFA.
func (s *Service) ResetMFA(ctx context.Context, id uuid.UUID) error {
	return s.store.ClearMFA(ctx, id)
}

// AdminSetPassword hashes and stores a new password without requiring the old one.
func (s *Service) AdminSetPassword(ctx context.Context, id uuid.UUID, plain string) error {
	if strings.TrimSpace(plain) == "" {
		return fmt.Errorf("%w: password is required", ErrValidation)
	}
	// Reset was the loosest of the four paths: it refused only a blank
	// password, so an administrator could reset an account to "b".
	if len(plain) < MinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters",
			ErrValidation, MinPasswordLength)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), s.hashCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	return s.store.AdminSetPassword(ctx, id, string(hash))
}

// MFA attempt policy.
//
// Deliberately not configurable: an operator raising the ceiling would be
// weakening a control without knowing it, and no legitimate workflow needs
// more than a handful of code entries. NIST SP 800-63B caps consecutive failed
// attempts per account; RFC 4226 section 7.3 requires throttling for exactly
// this reason, since a six-digit code is guessable at roughly 3e-6 per attempt
// and pquerna/otp accepts a +/-1 window, so three codes are live at once.
const (
	MFAMaxFailedAttempts = 5
	MFALockDuration      = 15 * time.Minute
)

// MinPasswordLength is the shortest password this application will store,
// wherever it is set.
//
// One number, because it was three: self-service change required eight, and
// admin create, admin reset and first-run setup each required one character —
// an administrator could create an account with the password "a", and did not
// have to be trying to. A minimum that applies on one of four paths is not a
// minimum.
const MinPasswordLength = 8

// ErrMFALocked reports that an account has spent its TOTP attempts.
var ErrMFALocked = errors.New("too many incorrect codes; try again later")

// ClaimMFAAttempt spends one of the account's TOTP attempts and reports
// ErrMFALocked when there are none left.
//
// Called BEFORE the code is checked, and that ordering is the control. The
// previous shape was CheckMFALock, then VerifyMFACode, then RecordMFAFailure:
// three statements with a window between the first and the last, so every
// request that started before the first UPDATE landed read "not locked" and
// went on to verify a guess. Measured against the real server: forty parallel
// wrong codes, thirty-six verified, limit five. An attacker who already holds
// the password — the exact case MFA exists for — got a few hundred guesses per
// fifteen-minute window instead of five.
//
// Counting before verifying means a correct code also costs an attempt. It
// does not matter: success clears the counter, so a legitimate user never
// meets the limit, and the alternative is a window an attacker can drive a
// bus through.
//
// The lock is time-based rather than administrator-cleared on purpose: it
// expires on its own, so a user locked out by a wrong code gets back in
// without needing anyone, and an administrator is not drawn into clearing MFA
// — which would leave the account open to whoever already knows the password
// until the legitimate user re-enrols.
func (s *Service) ClaimMFAAttempt(ctx context.Context, id uuid.UUID) error {
	attempts, lockedUntil, err := s.store.ClaimMFAAttempt(ctx, id, MFAMaxFailedAttempts, MFALockDuration)
	if err != nil {
		return err
	}
	// Strictly greater than: the attempt that spends the last of the budget
	// is still allowed to verify — it is the fifth of five, not the sixth —
	// and it is the one that sets the lock for everything after it.
	if attempts > MFAMaxFailedAttempts {
		return ErrMFALocked
	}
	if lockedUntil != nil && attempts >= MFAMaxFailedAttempts && time.Now().After(*lockedUntil) {
		// Cannot happen: the statement clears an expired lock and resets the
		// count in the same breath. Here so that a future edit to that SQL
		// which breaks the reset fails closed rather than open.
		return ErrMFALocked
	}
	return nil
}

// CheckMFALock reports whether the account is currently locked out of TOTP
// verification.
//
// Deprecated: racy when used as a gate. It reads the lock and returns, so
// several requests can pass it at once and each go on to check a code — see
// ClaimMFAAttempt, which is what the handlers use. Kept because removing an
// exported method is a separate commit; it has no callers outside tests.
func (s *Service) CheckMFALock(ctx context.Context, id uuid.UUID) error {
	_, lockedUntil, err := s.store.GetMFALock(ctx, id)
	if err != nil {
		return err
	}
	if lockedUntil != nil && time.Now().Before(*lockedUntil) {
		return ErrMFALocked
	}
	return nil
}

// RecordMFAFailure counts a wrong code and reports ErrMFALocked once the
// account has spent its attempts.
//
// Deprecated: counting after the check is the half of the race that let
// concurrent guesses through. Use ClaimMFAAttempt, which counts first.
//
// The lock is time-based rather than administrator-cleared on purpose: it
// expires on its own, so a user locked out by a wrong code gets back in
// without needing anyone, and an administrator is not drawn into clearing MFA
// — which would leave the account open to whoever already knows the password
// until the legitimate user re-enrols.
func (s *Service) RecordMFAFailure(ctx context.Context, id uuid.UUID) error {
	_, lockedUntil, err := s.store.RecordMFAFailure(ctx, id, MFAMaxFailedAttempts, MFALockDuration)
	if err != nil {
		return err
	}
	if lockedUntil != nil && time.Now().Before(*lockedUntil) {
		return ErrMFALocked
	}
	return nil
}

// ClearMFAFailures forgets prior failures after a correct code, per NIST SP
// 800-63B.
func (s *Service) ClearMFAFailures(ctx context.Context, id uuid.UUID) error {
	return s.store.ClearMFAFailures(ctx, id)
}

// ErrEmailTaken is another account already holding this address.
//
// Named so the handler can answer 409 rather than 500. An administrator
// typing an address that already exists is an ordinary mistake, and "an
// internal error occurred" is both wrong and unhelpful — the one thing they
// need to know is that the address is taken.
//
// It covers a deleted account too, because the unique constraint does: the
// row stays and keeps its address. That is worth knowing when re-hiring
// somebody, and it is why the message says so.
var ErrEmailTaken = errors.New("that email address is already in use, possibly by a deleted account")

// isUniqueViolation reports whether a store error is Postgres refusing a
// duplicate row. Matched on the SQLSTATE rather than the message, which is
// localised and names tables this layer should not be reading.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
