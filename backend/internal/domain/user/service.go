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
)

// Service orchestrates user-related business operations.
type Service struct {
	store Store
}

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

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
		return User{}, fmt.Errorf("invalid user: %w", err)
	}
	switch {
	case in.Password != "":
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return User{}, fmt.Errorf("hashing password: %w", err)
		}
		u.PasswordHash = string(hash)
	case in.PasswordHash != "":
		u.PasswordHash = in.PasswordHash
	}
	if err := s.store.Create(ctx, u); err != nil {
		return User{}, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

// SetPassword hashes and stores a new password for the given user.
func (s *Service) SetPassword(ctx context.Context, userID uuid.UUID, plain string) error {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	u.PasswordHash = string(hash)
	u.UpdatedAt = time.Now()
	return s.store.Update(ctx, u)
}

// VerifyPassword looks up a user by email and checks the plain-text password.
func (s *Service) VerifyPassword(ctx context.Context, email, plain string) (User, error) {
	u, err := s.store.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return User{}, fmt.Errorf("looking up user: %w", err)
	}
	if !u.IsActive() {
		return User{}, fmt.Errorf("user account is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plain)); err != nil {
		return User{}, fmt.Errorf("invalid credentials")
	}
	return u, nil
}

// EnrollMFA generates a TOTP secret for the user, stores it (unenrolled until
// confirmed), and returns the secret and a data URL for a QR code.
func (s *Service) EnrollMFA(ctx context.Context, userID uuid.UUID, issuer string) (secret, qrDataURL string, err error) {
	u, err := s.store.GetByID(ctx, userID)
	if err != nil {
		return "", "", err
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
	u.MFAEnabled = true
	u.UpdatedAt = time.Now()
	return s.store.Update(ctx, u)
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
		if err := s.store.Update(ctx, u); err != nil {
			return User{}, fmt.Errorf("updating OIDC user: %w", err)
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
			u.OIDCSubject = oidcSubject
			if displayName != "" {
				u.DisplayName = displayName
			}
			u.UpdatedAt = time.Now()
			if err := s.store.Update(ctx, u); err != nil {
				return User{}, fmt.Errorf("linking OIDC subject to user: %w", err)
			}
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
	u, err := s.store.GetBySAMLSubject(ctx, samlSubject)
	if err == nil {
		// Existing user — sync profile (domain restriction does not apply to existing users).
		if !u.IsActive() {
			return User{}, ErrUserDisabled
		}
		u.Email = strings.ToLower(strings.TrimSpace(email))
		u.DisplayName = strings.TrimSpace(displayName)
		u.UpdatedAt = time.Now()
		if err := s.store.Update(ctx, u); err != nil {
			return User{}, fmt.Errorf("updating SAML user: %w", err)
		}
		return u, nil
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

// HasUsers returns true when at least one user record exists.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	n, err := s.store.Count(ctx)
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
	return s.store.SoftDelete(ctx, id)
}

// Update persists changes to an existing user.
func (s *Service) Update(ctx context.Context, u User) error {
	if err := u.Validate(); err != nil {
		return fmt.Errorf("invalid user: %w", err)
	}
	u.UpdatedAt = time.Now()
	return s.store.Update(ctx, u)
}

// GetByIDAdmin returns the user with the given ID, including disabled users.
func (s *Service) GetByIDAdmin(ctx context.Context, id uuid.UUID) (User, error) {
	return s.store.GetByIDAdmin(ctx, id)
}

// ListAdmin returns all users including disabled ones.
func (s *Service) ListAdmin(ctx context.Context, limit, offset int) ([]User, error) {
	return s.store.ListAdmin(ctx, limit, offset)
}

// Disable marks a user account as disabled without deleting it.
func (s *Service) Disable(ctx context.Context, id uuid.UUID) error {
	return s.store.Disable(ctx, id)
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
		return fmt.Errorf("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	return s.store.AdminSetPassword(ctx, id, string(hash))
}
