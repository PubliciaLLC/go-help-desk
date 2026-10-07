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
	// ListAssignableStaff is the id-and-name list staff need to assign work.
	ListAssignableStaff(ctx context.Context) ([]AssignableStaff, error)
	// UpdateProfile writes only the address and the name, so an edit cannot
	// silently revert a role, a password or an MFA enrolment that changed
	// while the request was in flight.
	UpdateProfile(ctx context.Context, id uuid.UUID, email, displayName string) error
	// SetPasswordHash, SetMFA and SyncFederated each write the one thing they
	// name. The whole-row Update below carries a copy of every column, so a
	// caller that reads, thinks, and then writes puts back whatever changed
	// while it was thinking — and for a password change, the thinking is a
	// bcrypt hash the account holder chose the moment of.
	SetPasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	SetMFA(ctx context.Context, id uuid.UUID, secret string, enabled bool) error
	// SetFirstMFA adopts secret only if the account has no TOTP when the row
	// is written, and reports whether it did (#338).
	SetFirstMFA(ctx context.Context, id uuid.UUID, secret string) (bool, error)
	// EnableMFAIfStillEnrolled turns the flag on using the secret already on
	// the row, so a caller that read, validated a code, and then wrote does
	// not carry a copy of the secret across that gap. False means there was
	// no secret left to enable.
	EnableMFAIfStillEnrolled(ctx context.Context, id uuid.UUID) (bool, error)
	SyncFederated(ctx context.Context, id uuid.UUID, email, displayName string) error
	// AdoptOIDCSubject binds an OIDC subject to an account found by email
	// address, and reports whether it applied. The adoption rules live in
	// the statement, so an account promoted or disabled between the lookup
	// and the write is not adopted on the strength of the older read.
	AdoptOIDCSubject(ctx context.Context, id uuid.UUID, subject, displayName string) (bool, error)
	// EmailIsTaken covers deleted rows too, because the unique constraint
	// does. GetByEmail is the login lookup and hides them.
	EmailIsTaken(ctx context.Context, email string) (bool, error)
	// CountOtherActiveAdmins counts the administrators left if this one
	// stopped being one, so the last of them cannot be removed.
	// These three do their check and their write in one statement, so two of
	// them racing cannot both decide they are allowed. Each reports whether
	// it applied. Counting first and writing second lost that race: measured,
	// one administrator sending "remove Bob" and "remove me" together left
	// the instance with no administrator every time.
	DisableUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error)
	SoftDeleteUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error)
	SetRoleUnlessLastAdmin(ctx context.Context, id uuid.UUID, role string) (bool, error)

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
	// ListActiveAdmins returns every administrator who is neither disabled nor
	// soft-deleted, in full — not a count, unlike CountOtherActiveAdmins. The
	// SSO-settings guard (#300) has to know WHICH administrators would still
	// have a way to authenticate after a change, not merely how many remain.
	ListActiveAdmins(ctx context.Context) ([]User, error)
	Count(ctx context.Context) (int64, error)
	ClearMFA(ctx context.Context, id uuid.UUID) error
	AdminSetPassword(ctx context.Context, id uuid.UUID, hash string) error
}
