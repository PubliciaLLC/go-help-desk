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
		OidcSubject:  u.OIDCSubject,
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

func (s *Store) GetByOIDCSubject(ctx context.Context, subject string) (user.User, error) {

	row, err := s.q.GetUserByOIDCSubject(ctx, subject)

	if err != nil {
		return user.User{}, wrapNotFound(err, "user by OIDC subject", subject)
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
		OidcSubject:  u.OIDCSubject,
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

func (s *Store) Disable(ctx context.Context, id uuid.UUID) error {
	return s.q.DisableUser(ctx, id)
}

func (s *Store) Enable(ctx context.Context, id uuid.UUID) error {
	return s.q.EnableUser(ctx, id)
}

func (s *Store) ClearMFA(ctx context.Context, id uuid.UUID) error {
	return s.q.ClearMFA(ctx, id)
}

// ClaimMFAAttempt takes one attempt off the account's TOTP budget before the
// code is checked, and reports the count and the lock afterwards.
func (s *Store) ClaimMFAAttempt(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (int, *time.Time, error) {
	row, err := s.q.ClaimMFAAttempt(ctx, dbgen.ClaimMFAAttemptParams{
		ID:          id,
		MaxAttempts: int32(maxAttempts),
		LockSeconds: int32(lockFor / time.Second),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("claiming MFA attempt: %w", err)
	}
	return int(row.MfaFailedAttempts), database.TimePtr(row.MfaLockedUntil), nil
}

// RecordMFAFailure counts a failed TOTP attempt and locks the account once the
// threshold is reached, in one statement so concurrent attempts cannot both
// read the same count and both decide they are under the limit.
func (s *Store) RecordMFAFailure(ctx context.Context, id uuid.UUID, maxAttempts int, lockFor time.Duration) (int, *time.Time, error) {
	row, err := s.q.RecordMFAFailure(ctx, dbgen.RecordMFAFailureParams{
		ID:          id,
		MaxAttempts: int32(maxAttempts),
		LockSeconds: int32(lockFor / time.Second),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("recording MFA failure: %w", err)
	}
	return int(row.MfaFailedAttempts), database.TimePtr(row.MfaLockedUntil), nil
}

func (s *Store) ClearMFAFailures(ctx context.Context, id uuid.UUID) error {
	return s.q.ClearMFAFailures(ctx, id)
}

func (s *Store) GetMFALock(ctx context.Context, id uuid.UUID) (int, *time.Time, error) {
	row, err := s.q.GetMFALock(ctx, id)
	if err != nil {
		return 0, nil, fmt.Errorf("getting MFA lock: %w", err)
	}
	return int(row.MfaFailedAttempts), database.TimePtr(row.MfaLockedUntil), nil
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

// DisableUnlessLastAdmin, SoftDeleteUnlessLastAdmin and SetRoleUnlessLastAdmin
// each do their check and their write in one statement, so two of them racing
// cannot both decide they are allowed. Each reports whether it applied.
func (s *Store) DisableUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.appliedGuardedWrite(s.q.DisableUserUnlessLastAdmin(ctx, id))
}

func (s *Store) SoftDeleteUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.appliedGuardedWrite(s.q.SoftDeleteUserUnlessLastAdmin(ctx, id))
}

func (s *Store) SetRoleUnlessLastAdmin(ctx context.Context, id uuid.UUID, role string) (bool, error) {
	return s.appliedGuardedWrite(s.q.SetUserRoleUnlessLastAdmin(ctx, dbgen.SetUserRoleUnlessLastAdminParams{
		ID: id, Role: role,
	}))
}

// appliedGuardedWrite turns "no row came back" into "refused" rather than an
// error. These statements return the id when they applied and nothing when
// the guard stopped them, which database/sql reports as ErrNoRows.
func (s *Store) appliedGuardedWrite(_ uuid.UUID, err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	}
	return false, fmt.Errorf("guarded write: %w", err)
}

// CountOtherActiveAdmins counts the administrators this instance would still
// have if the given user stopped being one.
func (s *Store) CountOtherActiveAdmins(ctx context.Context, excluding uuid.UUID) (int64, error) {
	return s.q.CountOtherActiveAdmins(ctx, excluding)
}

// EmailIsTaken reports whether any row holds this address, deleted rows
// included — which is what the unique constraint covers.
func (s *Store) EmailIsTaken(ctx context.Context, email string) (bool, error) {
	taken, err := s.q.EmailIsTaken(ctx, email)
	if err != nil {
		return false, fmt.Errorf("checking address: %w", err)
	}
	return taken, nil
}

// ListAssignableStaff returns active staff and administrators, id and name
// only.
func (s *Store) ListAssignableStaff(ctx context.Context) ([]user.AssignableStaff, error) {
	rows, err := s.q.ListAssignableStaff(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing assignable staff: %w", err)
	}
	out := make([]user.AssignableStaff, len(rows))
	for i, r := range rows {
		out[i] = user.AssignableStaff{ID: r.ID, DisplayName: r.DisplayName}
	}
	return out, nil
}

// CountAll counts every user row, disabled and soft-deleted included.
func (s *Store) CountAll(ctx context.Context) (int64, error) {
	return s.q.CountAllUsers(ctx)
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
		OIDCSubject:  r.OidcSubject,
		Disabled:     r.Disabled,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		DeletedAt:    database.TimePtr(r.DeletedAt),
	}
}

// ErrNotFound is returned by Get* methods when the record does not exist.
// It wraps user.ErrNotFound so that domain code — which cannot import this
// package — can distinguish a missing row from a store failure.
var ErrNotFound = fmt.Errorf("user store: %w", user.ErrNotFound)

func wrapNotFound(err error, kind, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
	}
	return fmt.Errorf("getting %s %s: %w", kind, id, err)
}
