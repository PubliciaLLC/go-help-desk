package registration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ErrTokenExpired is returned when the verification token has passed its TTL.
var ErrTokenExpired = fmt.Errorf("verification token has expired")

// ErrNotFound is a pending registration that no longer exists: verified,
// replaced by a later signup for the same address, or swept after expiry.
var ErrNotFound = fmt.Errorf("pending registration not found")

// ErrInvalidEmail is returned when the address is not a single bare email
// address. Separate from ErrDomainNotAllowed so the caller can say which of the
// two it was.
var ErrInvalidEmail = fmt.Errorf("invalid email address")

// ErrAlreadyRegistered is a signup for an address that already has an
// account, deleted accounts included — the unique constraint covers those
// too, so a deleted account still owns its address.
//
// Never shown to the person signing up: the endpoint answers the same 202
// either way, so this cannot be used to find out who has an account here. It
// stops the verification email, which is what turned this into a dead end —
// the link arrived, the account could not be created, and the verify page
// said the token was invalid or already used.
var ErrAlreadyRegistered = fmt.Errorf("an account already exists for that address")

// ErrDisplayNameRequired is a verification with no name on it. Refused before
// anything is written, so the link still works (#374).
var ErrDisplayNameRequired = fmt.Errorf("display name is required")

// ErrPasswordTooShort is the refusal for a password chosen at verification
// below user.MinPasswordLength. The link stays usable, so the person can try
// again.
var ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", user.MinPasswordLength)

// ErrDomainNotAllowed is returned when the email domain is not permitted.
var ErrDomainNotAllowed = fmt.Errorf("email domain not allowed")

// ErrOpenRegistrationRequired is returned when self-signup is enabled but no
// domain restriction is set and open registration has not been explicitly enabled.
var ErrOpenRegistrationRequired = fmt.Errorf("open registration must be enabled to allow any email domain")

// userCreator is the subset of user.Service needed by the registration service.
type userCreator interface {
	Create(ctx context.Context, in user.CreateUserInput) (user.User, error)
	// EmailIsTaken so a signup for an address that already has an account can
	// be stopped before the verification email goes out, rather than failing
	// at the end of the flow with a message about the token. Deleted accounts
	// count: the unique constraint covers them, so one still owns its
	// address.
	EmailIsTaken(ctx context.Context, email string) (bool, error)
}

// Service handles the sign-up and email-verification workflow.
type Service struct {
	store  Store
	users  userCreator
	mailer Mailer
	// queue carries the verification email off the request (#348): the
	// notification outbox in production. Required, with no send-at-once
	// default, so wiring that forgets it does not compile (#361).
	queue   notification.Dispatcher
	baseURL string
}

// NewService returns a Service that queues verification email on queue.
func NewService(store Store, users userCreator, mailer Mailer, queue notification.Dispatcher, baseURL string) *Service {
	return &Service{store: store, users: users, mailer: mailer, queue: queue, baseURL: baseURL}
}

// PendingIDOf reads the pending registration an EventRegistrationVerify
// names. Its payload holds only that id: never the token or the address.
func PendingIDOf(ev notification.Event) (uuid.UUID, error) {
	raw, _ := ev.Payload["pending_id"].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("verification event without a pending registration id: %w", err)
	}
	return id, nil
}

// Register validates the request, stores a pending registration, and queues
// the verification email. allowedDomains and openReg come from admin settings.
//
// It takes no password (#360) and no display name (#374): both are chosen at
// Verify, by whoever can read the inbox. When signup carried them, a second
// signup for the same address replaced them, and the owner of the address,
// following the newest link, created their account with someone else's
// password, and later still with someone else's name.
func (s *Service) Register(ctx context.Context, email string, allowedDomains []string, openReg bool) error {

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

	// The same work for an address that already has an account and one that
	// does not, all the way to the response (#348). The address is checked,
	// the pending row written and one event queued in both cases; whether a
	// verification email goes out is decided when it would be sent
	// (SendVerification), off the request. Before this a fresh
	// address also dialled the mail server on the request and a taken one
	// returned at once, so the timing said who had an account even though
	// the 202 did not.
	//
	// A taken address gets a pending row that is never mailed, so its token
	// is never seen and it expires unused. Its account is untouched.
	taken, err := s.users.EmailIsTaken(ctx, email)
	if err != nil {
		return fmt.Errorf("checking the address: %w", err)
	}
	now := time.Now()
	pr := PendingRegistration{
		ID:        uuid.New(),
		Email:     email,
		Token:     uuid.New(),
		ExpiresAt: now.Add(tokenTTL),
		CreatedAt: now,
	}
	stored, err := s.store.Upsert(ctx, pr)
	if err != nil {
		return fmt.Errorf("storing pending registration: %w", err)
	}
	if err := s.queue.Dispatch(ctx, notification.Event{
		Type:       notification.EventRegistrationVerify,
		OccurredAt: now,
		Payload:    map[string]any{"pending_id": stored.ID.String()},
	}); err != nil {
		return fmt.Errorf("queueing verification email: %w", err)
	}
	if taken {
		// Returned for the caller's logs; the handler answers 202 either way.
		return ErrAlreadyRegistered
	}
	return nil
}

// SendVerification mails the verification link for a pending registration,
// at send time (#348). It re-reads the row, so the address and token are the
// ones stored now, and sends nothing for a row that is gone or expired or an
// address that has an account — including one created since the signup.
//
// An address that already has an account is refused here and not disclosed
// to the requester: no email goes out, so nobody follows a link and is told
// their token is invalid or already used. Deleted accounts count, as they do
// for the unique constraint.
func (s *Service) SendVerification(ctx context.Context, id uuid.UUID) error {
	pr, err := s.store.GetByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if time.Now().After(pr.ExpiresAt) {
		return nil
	}
	taken, err := s.users.EmailIsTaken(ctx, pr.Email)
	if err != nil {
		return fmt.Errorf("checking the address: %w", err)
	}
	if taken {
		return nil
	}
	// pr.Email, not what the request typed: the address on the row, read back
	// from the database. Same for the token.
	if err := s.mailer.SendVerificationEmail(pr.Email, pr.Token.String(), s.baseURL); err != nil {
		return fmt.Errorf("sending verification email: %w", err)
	}
	return nil
}

// Lookup returns the pending registration a verification link names, without
// using the link, so the verification page can show the address before asking
// for a password (#370). ErrNotFound is a link that is unknown, replaced or
// used, and ErrTokenExpired one past its TTL; any other error is a fault, not
// a verdict on the link. A link that passes can still fail at Verify — if the
// address has gained an account since, user.Create refuses it.
func (s *Service) Lookup(ctx context.Context, token uuid.UUID) (PendingRegistration, error) {
	pr, err := s.store.GetByToken(ctx, token)
	if err != nil {
		return PendingRegistration{}, err
	}
	if time.Now().After(pr.ExpiresAt) {
		return PendingRegistration{}, ErrTokenExpired
	}
	return pr, nil
}

// Verify looks up a token, checks expiry, creates the user account with the
// display name and password chosen now (#360, #374), and deletes the pending
// record. Returns the new User so the handler can write a session. A blank
// name or a password below the minimum is refused before anything is written,
// so the link still works.
func (s *Service) Verify(ctx context.Context, token uuid.UUID, displayName, password string) (user.User, error) {
	pr, err := s.store.GetByToken(ctx, token)
	if err != nil {
		return user.User{}, fmt.Errorf("token not found: %w", err)
	}
	if time.Now().After(pr.ExpiresAt) {
		return user.User{}, ErrTokenExpired
	}
	// user.Create applies both rules; checked here too so the refusal is one
	// the handler can name, rather than a generic validation error.
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return user.User{}, ErrDisplayNameRequired
	}
	if len(password) < user.MinPasswordLength {
		return user.User{}, ErrPasswordTooShort
	}

	u, err := s.users.Create(ctx, user.CreateUserInput{
		Email:       pr.Email,
		DisplayName: displayName,
		Role:        user.RoleUser,
		Password:    password,
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
