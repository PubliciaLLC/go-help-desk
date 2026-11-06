package passwordreset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Service handles "forgot password".
type Service struct {
	store  Store
	users  Users
	mailer Mailer
	// queue carries the request off the request thread: the notification
	// outbox in production. Required, as registration's is (#361).
	queue notification.Dispatcher
	audit audit.Store
	// samlEnabled reads the setting at the moment it is asked, so a change
	// takes effect without a restart.
	samlEnabled func(context.Context) bool
	baseURL     string
	log         *slog.Logger
}

// NewService returns a Service. log records a lost audit entry (see Reset); nil
// means slog.Default().
func NewService(store Store, users Users, mailer Mailer, queue notification.Dispatcher, auditStore audit.Store,
	samlEnabled func(context.Context) bool, baseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, users: users, mailer: mailer, queue: queue, audit: auditStore,
		samlEnabled: samlEnabled, baseURL: baseURL, log: log}
}

// Request queues a reset request for an address. It looks nothing up and
// writes nothing but the queue, so a request costs the same whether or not the
// address has an account; whether anything is mailed is decided when the mail
// would go (Send), off the request, as signup does (#348). The caller answers
// 202 whatever this returns for an address.
//
// A string that is not an address has nobody to mail and queues nothing. It
// returns ErrNotAnAddress so a caller that says a link was sent can tell it
// was not; the forgot-password endpoint ignores it, so the answer is the same
// either way.
func (s *Service) Request(ctx context.Context, email string) error {
	addr, err := user.ValidateEmail(email)
	if err != nil {
		return ErrNotAnAddress
	}
	if err := s.queue.Dispatch(ctx, notification.Event{
		Type:       notification.EventPasswordResetRequested,
		OccurredAt: time.Now(),
		Recipient:  addr,
	}); err != nil {
		return fmt.Errorf("queueing password reset: %w", err)
	}
	return nil
}

// Send is the send-time half of a request: it decides whether the address is
// to be mailed, and if so makes the token and mails the link. It mails nothing,
// and returns nil so the worker does not retry, for an address with no account,
// a disabled account, an account that cannot use a password because SAML limits
// password sign-in to administrators, an account that has no password (it signs
// in through an identity provider), or one whose budget is spent.
//
// allow charges and checks that budget for an account. It is the caller's, not
// this package's: the budget must be charged once per request and not once per
// delivery attempt, and that is known where the attempt number is.
//
// The token is made here, not at the request, so the outbox never holds one. It
// replaces any earlier link for the account. A failure to mail is returned for
// the worker to retry, and the retry makes a new token.
func (s *Service) Send(ctx context.Context, ev notification.Event, allow func(userID uuid.UUID) bool) error {
	if ev.Type != notification.EventPasswordResetRequested {
		return fmt.Errorf("not a password reset event: %q", ev.Type)
	}
	u, err := s.users.GetByEmail(ctx, ev.Recipient)
	if errors.Is(err, user.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("looking up the account: %w", err)
	}
	if !u.IsActive() || !user.IsLocalAuthAllowed(u, s.samlEnabled(ctx)) {
		return nil
	}
	// An account with no password signs in through an identity provider, and
	// "forgot password" is not for it: giving it one would put a way in beside
	// the provider's MFA and conditional access. Tested as sso_guard.go tests it.
	if u.PasswordHash == "" {
		return nil
	}
	if !allow(u.ID) {
		return nil
	}
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		return err
	}
	if err := s.store.Issue(ctx, u.ID, hashed, TokenTTL); err != nil {
		return fmt.Errorf("storing reset token: %w", err)
	}
	// u.Email, the address on the account, not what was typed.
	if err := s.mailer.SendPasswordResetEmail(ctx, u.Email, raw, s.baseURL); err != nil {
		return fmt.Errorf("sending password reset email: %w", err)
	}
	return nil
}

// Reset spends a link and sets the password it was mailed for. A password the
// rules refuse is refused before the link is touched, so it still works. Every
// session the account holds ends with the change.
//
// ErrTokenInvalid is a verdict on the link; any other error is a fault.
func (s *Service) Reset(ctx context.Context, token, password string) error {
	if err := user.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := s.users.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	// Detached from here on: the password is changed once Redeem returns, and
	// the person hanging up must not decide whether that leaves a trace, or
	// turn a reset that landed into a failed one.
	ctx = context.WithoutCancel(ctx)
	id, err := s.store.Redeem(ctx, auth.HashToken(token), hash)
	if err != nil {
		return err
	}
	s.writeAudit(ctx, audit.Entry{
		// No actor: nobody was signed in, and naming the account would record
		// a claim (the same reasoning as the reset-factors command, #306).
		EntityType: "user",
		EntityID:   id,
		Action:     "password_reset_by_email",
		After:      map[string]any{"sessions_revoked": true},
	})
	return nil
}

// writeAudit records e. A failure is logged and swallowed, as
// user.Service.writeAuditEntry does for the administrator's reset, and for the
// same reason: the password has already changed, and reporting that as failed
// over a logging hiccup would be the wrong trade (docs/DESIGN.md, #306).
func (s *Service) writeAudit(ctx context.Context, e audit.Entry) {
	e.ID = uuid.New()
	e.CreatedAt = time.Now()
	if err := s.audit.Create(ctx, e); err != nil {
		s.log.ErrorContext(ctx, "writing audit entry failed",
			"entity_type", e.EntityType, "entity_id", e.EntityID, "action", e.Action, "error", err)
	}
}
