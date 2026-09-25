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

// ErrInvalidEmail is returned when the address is not a single bare email
// address. Separate from ErrDomainNotAllowed so the caller can say which of the
// two it was.
var ErrInvalidEmail = fmt.Errorf("invalid email address")

// ErrAlreadyRegistered is a signup for an address that already has an
// account.
//
// Never shown to the person signing up: the endpoint answers the same 202
// either way, so this cannot be used to find out who has an account here. It
// stops the verification email, which is what turned this into a dead end —
// the link arrived, the account could not be created, and the verify page
// said the token was invalid or already used.
var ErrAlreadyRegistered = fmt.Errorf("an account already exists for that address")

// ErrDisplayNameRequired is a signup with no name on it.
//
// Refused at registration rather than at verification, where the same rule
// already applied: by then the person has been told they are registered and
// sent a link that cannot work.
var ErrDisplayNameRequired = fmt.Errorf("display name is required")

// ErrPasswordTooShort is the refusal for a signup password below
// user.MinPasswordLength.
var ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", user.MinPasswordLength)

// ErrDomainNotAllowed is returned when the email domain is not permitted.
var ErrDomainNotAllowed = fmt.Errorf("email domain not allowed")

// ErrOpenRegistrationRequired is returned when self-signup is enabled but no
// domain restriction is set and open registration has not been explicitly enabled.
var ErrOpenRegistrationRequired = fmt.Errorf("open registration must be enabled to allow any email domain")

// userCreator is the subset of user.Service needed by the registration service.
type userCreator interface {
	Create(ctx context.Context, in user.CreateUserInput) (user.User, error)
	// GetByEmail so a signup for an address that already has an account can
	// be stopped before the verification email goes out, rather than failing
	// at the end of the flow with a message about the token.
	GetByEmail(ctx context.Context, email string) (user.User, error)
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
	displayName = strings.TrimSpace(displayName)

	// Validated before anything is stored. isEmailDomainAllowed does not
	// inspect the address when open registration is on — it returns openReg
	// without looking — so without this any string at all was accepted, written
	// to pending_registrations, and became a user account on verification.
	email, err := user.ValidateEmail(email)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidEmail, err)
	}

	if !isEmailDomainAllowed(email, allowedDomains, openReg) {
		if len(allowedDomains) == 0 {
			return ErrOpenRegistrationRequired
		}
		return ErrDomainNotAllowed
	}

	// The fifth path that sets a password, and the one that was missed when
	// the minimum was made one rule. Nothing checked the length here, and
	// Verify creates the account from the stored hash — which skips the check
	// in user.Service.Create — so a signup with an EMPTY password produced a
	// real account whose login accepted an empty password. Signup is off by
	// default, which was the only thing standing in front of it.
	if len(password) < user.MinPasswordLength {
		return ErrPasswordTooShort
	}
	// A display name is required by user.Validate, which runs at Verify —
	// long after the person has been told their registration was accepted and
	// an email has been sent. Without this they follow the link and are told
	// the token is invalid or already used, which is neither. Refuse it here,
	// where they can still fix it.
	if displayName == "" {
		return ErrDisplayNameRequired
	}
	// An address that already has an account is refused HERE and not
	// disclosed to the requester — the 202 is deliberately the same either
	// way, so signing up is not a way to find out who has an account. What
	// changes is that the verification email is not sent, so nobody follows a
	// link and is told their token is invalid or already used, which it is
	// not. Same shape as the display-name case above: fail where the failure
	// is true rather than where it is confusing.
	if _, err := s.users.GetByEmail(ctx, email); err == nil {
		return ErrAlreadyRegistered
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

	// stored.Email, not email: what goes into the message is the address that
	// is actually on the row, read back from the database, not the string the
	// request supplied. Same reason stored.Token is used rather than pr.Token.
	if err := s.mailer.SendVerificationEmail(stored.Email, stored.Token.String(), s.baseURL); err != nil {
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
