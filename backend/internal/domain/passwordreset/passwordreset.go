// Package passwordreset lets a person who cannot sign in choose a new password
// from a link mailed to them (#451).
//
// The link never signs anybody in. Setting the password ends every session the
// account holds and sends the person back to the sign-in page, where they use
// the new password and their second factor, if they have one. A reset link is
// a credential that sits in an inbox; if it also skipped the second factor, a
// stolen inbox would be a stolen account.
package passwordreset

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// TokenTTL is how long a link works. Short, because the link is a password in
// waiting; long enough to read a mail that arrives a few minutes late.
const TokenTTL = 30 * time.Minute

// ErrNotAnAddress is Request's answer for a string it will not queue: nothing is
// mailed for it. The forgot-password endpoint ignores it, so what a requester
// is told does not depend on the address.
var ErrNotAnAddress = errors.New("not an address this server will mail")

// ErrTokenInvalid is a link that does not work: unknown, already used,
// replaced by a later one, expired, or for an account that can no longer have
// its password set. One error for all of them, so a guess is told nothing.
var ErrTokenInvalid = errors.New("password reset link is invalid, expired or already used")

// ErrPasswordTooShort and ErrPasswordTooLong are user.ValidatePassword's
// refusals. The link is not used up by them, so the person can try again.
var (
	ErrPasswordTooShort = user.ErrPasswordTooShort
	ErrPasswordTooLong  = user.ErrPasswordTooLong
)

// Store persists reset tokens. An account has at most one live token.
type Store interface {
	// Issue makes tokenHash the account's one live token, replacing any
	// earlier, good for ttl by the database's clock. Only the hash is stored.
	Issue(ctx context.Context, userID uuid.UUID, tokenHash string, ttl time.Duration) error

	// Redeem spends the token and, in the same statement, sets the account's
	// password hash and ends every session it holds, so a link cannot be used
	// twice and a password cannot change while an old session survives. It
	// returns the account, or ErrTokenInvalid when nothing matched.
	Redeem(ctx context.Context, tokenHash, passwordHash string) (uuid.UUID, error)
}

// Users is what the service needs of user.Service.
type Users interface {
	// GetByEmail finds a live account by address. It does not find a deleted
	// account or a service account.
	GetByEmail(ctx context.Context, email string) (user.User, error)
	HashPassword(plain string) (string, error)
}

// Mailer sends the link.
type Mailer interface {
	SendPasswordResetEmail(ctx context.Context, to, token, baseURL string) error
}
