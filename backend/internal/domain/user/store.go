package user

import (
	"context"
	"errors"

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
