package user

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for users.
// Implementations live in internal/database/userstore.
type Store interface {
	Create(ctx context.Context, u User) error
	GetByID(ctx context.Context, id uuid.UUID) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	GetBySAMLSubject(ctx context.Context, subject string) (User, error)
	Update(ctx context.Context, u User) error
	SoftDelete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, limit, offset int) ([]User, error)
	Count(ctx context.Context) (int64, error)
}
