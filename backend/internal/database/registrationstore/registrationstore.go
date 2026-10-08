// Package registrationstore implements domain/registration.Store against PostgreSQL via sqlc.
package registrationstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
)

// Store implements registration.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Upsert(ctx context.Context, r registration.PendingRegistration) (registration.PendingRegistration, error) {
	row, err := s.q.UpsertPendingRegistration(ctx, dbgen.UpsertPendingRegistrationParams{
		ID:          r.ID,
		Email:       r.Email,
		DisplayName: r.DisplayName,
		Token:       r.Token,
		ExpiresAt:   r.ExpiresAt,
		CreatedAt:   r.CreatedAt,
	})
	if err != nil {
		return registration.PendingRegistration{}, fmt.Errorf("upserting pending registration: %w", err)
	}
	return fromRow(row), nil
}

func (s *Store) GetByToken(ctx context.Context, token uuid.UUID) (registration.PendingRegistration, error) {
	row, err := s.q.GetPendingRegistrationByToken(ctx, token)
	if err != nil {
		return registration.PendingRegistration{}, fmt.Errorf("getting pending registration by token: %w", err)
	}
	return fromRow(row), nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (registration.PendingRegistration, error) {
	row, err := s.q.GetPendingRegistrationByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return registration.PendingRegistration{}, registration.ErrNotFound
	}
	if err != nil {
		return registration.PendingRegistration{}, fmt.Errorf("getting pending registration: %w", err)
	}
	return fromRow(row), nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.DeletePendingRegistration(ctx, id)
}

func fromRow(r dbgen.PendingRegistration) registration.PendingRegistration {
	return registration.PendingRegistration{
		ID:          r.ID,
		Email:       r.Email,
		DisplayName: r.DisplayName,
		Token:       r.Token,
		ExpiresAt:   r.ExpiresAt,
		CreatedAt:   r.CreatedAt,
	}
}
