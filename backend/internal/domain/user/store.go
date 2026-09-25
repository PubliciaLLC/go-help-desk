package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is the sentinel every Store wraps when a lookup finds no row.
// It lives here, not in the store package, because internal/domain must not
// import internal/database: the service needs to tell "no such record" apart
// from "the store failed", and this is the only shared vocabulary for it.
var ErrNotFound = errors.New("not found")

// Store is the persistence interface for users.
// Implementations live in internal/database/userstore.
type Store interface {
	// MFA attempt tracking. Durable rather than in memory, because a counter
	// that a restart clears is not a limit on a six-digit secret.
	ClaimMFAAttempt(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (attempts int, lockedUntil *time.Time, err error)
	RecordMFAFailure(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (attempts int, lockedUntil *time.Time, err error)
	ClearMFAFailures(ctx context.Context, id uuid.UUID) error
	GetMFALock(ctx context.Context, id uuid.UUID) (attempts int, lockedUntil *time.Time, err error)

	// CountAll counts every row, including disabled and soft-deleted
	// accounts. Count, which excludes them, is the wrong question for
	// "has this instance ever been set up".
	CountAll(ctx context.Context) (int64, error)
	// CountOtherActiveAdmins counts the administrators left if this one
	// stopped being one, so the last of them cannot be removed.
	CountOtherActiveAdmins(ctx context.Context, excluding uuid.UUID) (int64, error)

	Create(ctx context.Context, u User) error
	GetByID(ctx context.Context, id uuid.UUID) (User, error)
	GetByIDAdmin(ctx context.Context, id uuid.UUID) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	GetBySAMLSubject(ctx context.Context, subject string) (User, error)
	GetByOIDCSubject(ctx context.Context, subject string) (User, error)
	Update(ctx context.Context, u User) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
	Restore(ctx context.Context, id uuid.UUID) error
	Disable(ctx context.Context, id uuid.UUID) error
	Enable(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, limit, offset int) ([]User, error)
	ListAdmin(ctx context.Context, limit, offset int) ([]User, error)
	Count(ctx context.Context) (int64, error)
	ClearMFA(ctx context.Context, id uuid.UUID) error
	AdminSetPassword(ctx context.Context, id uuid.UUID, hash string) error
}
