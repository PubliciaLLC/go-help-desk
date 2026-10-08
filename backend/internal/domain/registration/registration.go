// Package registration handles self-service user sign-up with email verification.
package registration

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const tokenTTL = 24 * time.Hour

// PendingRegistration is a not-yet-verified sign-up request.
//
// It holds no password (#360). That is chosen on the verification page, so
// only whoever reads the inbox chooses it; held here, a second signup for the
// same address could replace it with its own.
type PendingRegistration struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Token       uuid.UUID
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// Store persists pending registrations.
type Store interface {
	Upsert(ctx context.Context, r PendingRegistration) (PendingRegistration, error)
	GetByToken(ctx context.Context, token uuid.UUID) (PendingRegistration, error)
	// GetByID returns ErrNotFound when no row has the id.
	GetByID(ctx context.Context, id uuid.UUID) (PendingRegistration, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// Mailer sends transactional email for registration verification.
type Mailer interface {
	SendVerificationEmail(to, token, baseURL string) error
}
