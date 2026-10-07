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

	"github.com/jackc/pgx/v5/pgconn"
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

// ClearMFA clears the TOTP columns only.
//
// Deprecated: nothing in the server calls it any more. Both reset paths need
// the passkeys gone and the sessions ended in the same breath, which is
// ClearFactors; clearing one factor of two is how "Reset MFA" left a
// passkey-only account locked out (#307). Kept, not deleted, so this change
// breaks nothing; remove it in a separate commit.
func (s *Store) ClearMFA(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.ClearMFA(ctx, id)
	if err != nil {
		return fmt.Errorf("clearing MFA for user %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, id)
	}
	return nil
}

// ClearFactors removes the account's authenticator and every passkey and ends
// every session it holds, in one statement, and reports how many passkeys were
// removed. See the ClearFactors query for why it is one statement.
func (s *Store) ClearFactors(ctx context.Context, id uuid.UUID) (int, error) {
	row, err := s.q.ClearFactors(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("clearing factors for user %s: %w", id, err)
	}
	if row.UsersCleared == 0 {
		return 0, fmt.Errorf("%w: user %s", ErrNotFound, id)
	}
	return int(row.PasskeysRemoved), nil
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
	n, err := s.q.AdminSetPassword(ctx, dbgen.AdminSetPasswordParams{ID: id, PasswordHash: hash})
	if err != nil {
		return fmt.Errorf("setting password for user %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: user %s", ErrNotFound, id)
	}
	return nil
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
	return s.guardedWrite(func() (uuid.UUID, error) {
		return s.q.DisableUserUnlessLastAdmin(ctx, id)
	})
}

func (s *Store) SoftDeleteUnlessLastAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.guardedWrite(func() (uuid.UUID, error) {
		return s.q.SoftDeleteUserUnlessLastAdmin(ctx, id)
	})
}

func (s *Store) SetRoleUnlessLastAdmin(ctx context.Context, id uuid.UUID, role string) (bool, error) {
	return s.guardedWrite(func() (uuid.UUID, error) {
		return s.q.SetUserRoleUnlessLastAdmin(ctx, dbgen.SetUserRoleUnlessLastAdminParams{
			ID: id, Role: role,
		})
	})
}

// guardedWrite runs one of the three statements above and reads its answer.
//
// It retries once on a deadlock, for the same reason txrunner.InTx does, and
// this is the side that needs it more. Each of these statements locks every
// administrator row before touching the target; an assignment running at the
// same time share-locks the assignee first and then writes an audit row whose
// actor may be an administrator. When those two orders cross, Postgres kills
// whichever transaction has waited longest — and that is this one, because it
// starts by locking the whole administrator set and waits there. Without a
// retry the assignment commits and the administrator gets "an internal error
// occurred" for a disable that did nothing.
//
// Once, not in a loop, for the reason given in txrunner.
//
// "No row came back" is not an error: these statements return the id when
// they applied and nothing when the guard stopped them, which database/sql
// reports as ErrNoRows.
func (s *Store) guardedWrite(write func() (uuid.UUID, error)) (bool, error) {
	_, err := write()
	if isDeadlock(err) {
		_, err = write()
	}
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	}
	return false, fmt.Errorf("guarded write: %w", err)
}

// isDeadlock reports whether Postgres killed this statement to break a lock
// cycle. SQLSTATE 40P01, matched on the code rather than the message.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// CountOtherActiveAdmins counts the administrators this instance would still
// have if the given user stopped being one.
func (s *Store) CountOtherActiveAdmins(ctx context.Context, excluding uuid.UUID) (int64, error) {
	return s.q.CountOtherActiveAdmins(ctx, excluding)
}

// ListActiveAdmins returns every administrator who is neither disabled nor
// soft-deleted.
func (s *Store) ListActiveAdmins(ctx context.Context) ([]user.User, error) {
	rows, err := s.q.ListActiveAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing active admins: %w", err)
	}
	out := make([]user.User, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
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

// SetPasswordHash, SetMFA and SyncFederated each write the one thing they
// name, so a slow caller cannot write a stale copy of everything else back.
func (s *Store) SetPasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	if err := s.q.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: id, PasswordHash: hash}); err != nil {
		return fmt.Errorf("setting password: %w", err)
	}
	return nil
}

func (s *Store) SetMFA(ctx context.Context, id uuid.UUID, secret string, enabled bool) error {
	if err := s.q.SetUserMFA(ctx, dbgen.SetUserMFAParams{ID: id, MfaSecret: secret, MfaEnabled: enabled}); err != nil {
		return fmt.Errorf("setting MFA: %w", err)
	}
	return nil
}

func (s *Store) SetFirstMFA(ctx context.Context, id uuid.UUID, secret string) (bool, error) {
	n, err := s.q.SetFirstUserMFA(ctx, dbgen.SetFirstUserMFAParams{ID: id, MfaSecret: secret})
	if err != nil {
		return false, fmt.Errorf("setting first MFA: %w", err)
	}
	return n == 1, nil
}

func (s *Store) SyncFederated(ctx context.Context, id uuid.UUID, email, displayName string) error {
	err := s.q.SyncFederatedUser(ctx, dbgen.SyncFederatedUserParams{
		ID: id, Email: email, DisplayName: displayName,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("syncing federated user: %w", err)
	}
	return nil
}

// AdoptOIDCSubject binds an OIDC subject to an existing account. An empty
// displayName leaves the stored name alone — an identity provider that stops
// releasing the attribute must not blank it.
func (s *Store) AdoptOIDCSubject(ctx context.Context, id uuid.UUID, subject, displayName string) (bool, error) {
	name := sql.NullString{String: displayName, Valid: displayName != ""}
	_, err := s.q.AdoptUserByOIDCSubject(ctx, dbgen.AdoptUserByOIDCSubjectParams{
		ID: id, OidcSubject: subject, DisplayName: name,
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		// The row no longer meets the adoption rules. Refused, not broken.
		return false, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Another account already carries this subject. Same shape as the
		// address collision: tell the caller which rule it hit.
		return false, user.ErrAccountLinkRefused
	}
	return false, fmt.Errorf("adopting OIDC subject: %w", err)
}

// EnableMFAIfStillEnrolled turns the flag on without carrying a copy of the
// secret, and reports whether a secret was still there to enable.
func (s *Store) EnableMFAIfStillEnrolled(ctx context.Context, id uuid.UUID) (bool, error) {
	_, err := s.q.EnableMFAIfStillEnrolled(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		// No secret on the row any more. Refused, not broken.
		return false, nil
	}
	return false, fmt.Errorf("enabling MFA: %w", err)
}

// UpdateProfile writes only the address and the name.
func (s *Store) UpdateProfile(ctx context.Context, id uuid.UUID, email, displayName string) error {
	err := s.q.UpdateUserProfile(ctx, dbgen.UpdateUserProfileParams{
		ID: id, Email: email, DisplayName: displayName,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("updating profile: %w", err)
	}
	return nil
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
		out[i] = user.AssignableStaff{ID: r.ID, DisplayName: r.DisplayName, Assignable: r.Assignable.Bool}
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
