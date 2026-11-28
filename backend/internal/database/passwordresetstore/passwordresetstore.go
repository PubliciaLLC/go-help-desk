// Package passwordresetstore implements domain/passwordreset.Store against PostgreSQL via sqlc.
package passwordresetstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/passwordreset"
)

// Store implements passwordreset.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Issue(ctx context.Context, userID uuid.UUID, tokenHash string, ttl time.Duration) error {
	err := s.q.IssuePasswordResetToken(ctx, dbgen.IssuePasswordResetTokenParams{
		UserID:     userID,
		TokenHash:  tokenHash,
		TtlSeconds: int32(ttl / time.Second),
	})
	if err != nil {
		return fmt.Errorf("issuing password reset token: %w", err)
	}
	return nil
}

func (s *Store) Redeem(ctx context.Context, tokenHash, passwordHash string) (uuid.UUID, error) {
	id, err := s.q.RedeemPasswordResetToken(ctx, dbgen.RedeemPasswordResetTokenParams{
		TokenHash:    tokenHash,
		PasswordHash: passwordHash,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, passwordreset.ErrTokenInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("redeeming password reset token: %w", err)
	}
	return id, nil
}
