package user

import (
	"context"
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
	Email       string
	DisplayName string
	Role        Role
	Password    string // plain text; empty if SAML-only
	SAMLSubject string // empty if local-only
}

// Create validates and persists a new user, hashing the password if provided.
func (s *Service) Create(ctx context.Context, in CreateUserInput) (User, error) {
	u := User{
		ID:          uuid.New(),
		Email:       strings.ToLower(strings.TrimSpace(in.Email)),
		DisplayName: strings.TrimSpace(in.DisplayName),
		Role:        in.Role,
		SAMLSubject: in.SAMLSubject,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := u.Validate(); err != nil {
		return User{}, fmt.Errorf("invalid user: %w", err)
	}
	if in.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return User{}, fmt.Errorf("hashing password: %w", err)
		}
		u.PasswordHash = string(hash)
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

// UpsertSAMLUser creates or updates a user record based on a SAML assertion.
// If a user with the given SAML subject already exists, their email and
// display name are updated. If not, a new user with the User role is created.
func (s *Service) UpsertSAMLUser(ctx context.Context, samlSubject, email, displayName string) (User, error) {
	u, err := s.store.GetBySAMLSubject(ctx, samlSubject)
	if err == nil {
		// Existing user — sync profile.
		u.Email = strings.ToLower(strings.TrimSpace(email))
		u.DisplayName = strings.TrimSpace(displayName)
		u.UpdatedAt = time.Now()
		if err := s.store.Update(ctx, u); err != nil {
			return User{}, fmt.Errorf("updating SAML user: %w", err)
		}
		return u, nil
	}
	// Not found — create.
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
