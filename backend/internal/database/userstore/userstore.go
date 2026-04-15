// Package userstore implements domain/user.Store against PostgreSQL via sqlc.
package userstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Store implements user.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Create(ctx context.Context, u user.User) error {
	return s.q.CreateUser(ctx, dbgen.CreateUserParams{
		ID:           u.ID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		Role:         string(u.Role),
		PasswordHash: u.PasswordHash,
		MfaSecret:    u.MFASecret,
		MfaEnabled:   u.MFAEnabled,
		SamlSubject:  u.SAMLSubject,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	})
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (user.User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		return user.User{}, wrapNotFound(err, "user", id.String())
	}
	return fromRow(row), nil
}

func (s *Store) GetByEmail(ctx context.Context, email string) (user.User, error) {
	row, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		return user.User{}, wrapNotFound(err, "user by email", email)
	}
	return fromRow(row), nil
}

func (s *Store) GetBySAMLSubject(ctx context.Context, subject string) (user.User, error) {
	row, err := s.q.GetUserBySAMLSubject(ctx, subject)
	if err != nil {
		return user.User{}, wrapNotFound(err, "user by SAML subject", subject)
	}
	return fromRow(row), nil
}

func (s *Store) Update(ctx context.Context, u user.User) error {
	return s.q.UpdateUser(ctx, dbgen.UpdateUserParams{
		ID:           u.ID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		Role:         string(u.Role),
		PasswordHash: u.PasswordHash,
		MfaSecret:    u.MFASecret,
		MfaEnabled:   u.MFAEnabled,
		SamlSubject:  u.SAMLSubject,
		UpdatedAt:    time.Now(),
	})
}

func (s *Store) GetByIDAdmin(ctx context.Context, id uuid.UUID) (user.User, error) {
	row, err := s.q.GetUserByIDAdmin(ctx, id)
	if err != nil {
		return user.User{}, wrapNotFound(err, "user", id.String())
	}
	return fromRow(row), nil
}

func (s *Store) SoftDelete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteUser(ctx, id)
}

func (s *Store) Restore(ctx context.Context, id uuid.UUID) error {
	return s.q.RestoreUser(ctx, id)
}

func (s *Store) ClearMFA(ctx context.Context, id uuid.UUID) error {
	return s.q.ClearMFA(ctx, id)
}

func (s *Store) AdminSetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	return s.q.AdminSetPassword(ctx, dbgen.AdminSetPasswordParams{ID: id, PasswordHash: hash})
}

func (s *Store) List(ctx context.Context, limit, offset int) ([]user.User, error) {
	rows, err := s.q.ListUsers(ctx, dbgen.ListUsersParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	out := make([]user.User, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *Store) ListAdmin(ctx context.Context, limit, offset int) ([]user.User, error) {
	rows, err := s.q.ListUsersAdmin(ctx, dbgen.ListUsersAdminParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing users (admin): %w", err)
	}
	out := make([]user.User, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *Store) Count(ctx context.Context) (int64, error) {
	return s.q.CountUsers(ctx)
}

// fromRow converts a dbgen.User to domain user.User.
func fromRow(r dbgen.User) user.User {
	return user.User{
		ID:           r.ID,
		Email:        r.Email,
		DisplayName:  r.DisplayName,
		Role:         user.Role(r.Role),
		PasswordHash: r.PasswordHash,
		MFASecret:    r.MfaSecret,
		MFAEnabled:   r.MfaEnabled,
		SAMLSubject:  r.SamlSubject,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		DeletedAt:    database.TimePtr(r.DeletedAt),
	}
}

// ErrNotFound is returned by Get* methods when the record does not exist.
var ErrNotFound = errors.New("not found")

func wrapNotFound(err error, kind, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
	}
	return fmt.Errorf("getting %s %s: %w", kind, id, err)
}
