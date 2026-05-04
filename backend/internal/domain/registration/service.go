package registration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ErrTokenExpired is returned when the verification token has passed its TTL.
var ErrTokenExpired = fmt.Errorf("verification token has expired")

// ErrDomainNotAllowed is returned when the email domain is not permitted.
var ErrDomainNotAllowed = fmt.Errorf("email domain not allowed")

// ErrOpenRegistrationRequired is returned when self-signup is enabled but no
// domain restriction is set and open registration has not been explicitly enabled.
var ErrOpenRegistrationRequired = fmt.Errorf("open registration must be enabled to allow any email domain")

// userCreator is the subset of user.Service needed by the registration service.
type userCreator interface {
	Create(ctx context.Context, in user.CreateUserInput) (user.User, error)
}

// Service handles the sign-up and email-verification workflow.
type Service struct {
	store   Store
	users   userCreator
	mailer  Mailer
	baseURL string
}

// NewService returns a Service.
func NewService(store Store, users userCreator, mailer Mailer, baseURL string) *Service {
	return &Service{store: store, users: users, mailer: mailer, baseURL: baseURL}
}

// Register validates the request, stores a pending registration, and sends the
// verification email. allowedDomains and openReg come from admin settings.
func (s *Service) Register(ctx context.Context, email, displayName, password string, allowedDomains []string, openReg bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)

	if !isEmailDomainAllowed(email, allowedDomains, openReg) {
		if len(allowedDomains) == 0 {
			return ErrOpenRegistrationRequired
		}
		return ErrDomainNotAllowed
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	now := time.Now()
	pr := PendingRegistration{
		ID:           uuid.New(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: string(hash),
		Token:        uuid.New(),
		ExpiresAt:    now.Add(tokenTTL),
		CreatedAt:    now,
	}

	stored, err := s.store.Upsert(ctx, pr)
	if err != nil {
		return fmt.Errorf("storing pending registration: %w", err)
	}

	if err := s.mailer.SendVerificationEmail(email, stored.Token.String(), s.baseURL); err != nil {
		// Non-fatal: log-worthy but don't expose SMTP failures to callers.
		return fmt.Errorf("sending verification email: %w", err)
	}
	return nil
}

// Verify looks up a token, checks expiry, creates the user account, and deletes
// the pending record. Returns the new User so the handler can write a session.
func (s *Service) Verify(ctx context.Context, token uuid.UUID) (user.User, error) {
	pr, err := s.store.GetByToken(ctx, token)
	if err != nil {
		return user.User{}, fmt.Errorf("token not found: %w", err)
	}
	if time.Now().After(pr.ExpiresAt) {
		return user.User{}, ErrTokenExpired
	}

	u, err := s.users.Create(ctx, user.CreateUserInput{
		Email:        pr.Email,
		DisplayName:  pr.DisplayName,
		Role:         user.RoleUser,
		PasswordHash: pr.PasswordHash, // already bcrypt-hashed at registration time
	})
	if err != nil {
		return user.User{}, fmt.Errorf("creating user: %w", err)
	}

	if err := s.store.Delete(ctx, pr.ID); err != nil {
		// Non-fatal: stale pending rows are harmless but log-worthy.
		_ = err
	}
	return u, nil
}

// isEmailDomainAllowed returns true when the email domain is in allowedDomains,
// or when allowedDomains is empty and openReg is true.
func isEmailDomainAllowed(email string, allowed []string, openReg bool) bool {
	if len(allowed) == 0 {
		return openReg
	}
	parts := strings.SplitN(email, "@", 2)
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
