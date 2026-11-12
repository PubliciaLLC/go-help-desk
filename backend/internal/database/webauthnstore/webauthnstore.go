// Package webauthnstore implements domain/webauthn.Store against PostgreSQL
// via sqlc.
package webauthnstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
)

// Store implements webauthn.Store.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil: see database.InTx
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB is New with db for CreateFirstFactor to begin its transaction on.
// Production uses it; the integration harness uses New, and see database.InTx
// for what that does not exercise.
func NewWithDB(q *dbgen.Queries, db *sql.DB) *Store { return &Store{q: q, db: db} }

// CreateFirstFactor writes the credential only if the account holds no second
// factor, under the user row lock LockUserForFactorWrite takes (#617). Of a
// first TOTP and a first passkey, whichever gets the row second sees what the
// first wrote and writes nothing.
func (s *Store) CreateFirstFactor(ctx context.Context, c webauthn.Credential) error {
	return database.InTx(ctx, s.db, s.q, func(q *dbgen.Queries) error {
		// No row means no account, or a soft-deleted one (the lock query
		// skips those): nothing to protect, so the insert below runs unguarded.
		// A user that never existed fails it on the foreign key, as in Create.
		// A soft-deleted user's row is still there and still satisfies the key,
		// so the credential IS written, onto an account that cannot sign in.
		if _, err := q.LockUserForFactorWrite(ctx, c.UserID); err == nil {
			protected, err := accountHoldsAFactor(ctx, q, c.UserID)
			if err != nil {
				return err
			}
			if protected {
				return webauthn.ErrAlreadyProtected
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("locking account: %w", err)
		}
		return (&Store{q: q}).Create(ctx, c)
	})
}

// accountHoldsAFactor asks, in statements of its own, after the lock is held;
// see LockUserForFactorWrite for why it cannot be one statement with it.
func accountHoldsAFactor(ctx context.Context, q *dbgen.Queries, userID uuid.UUID) (bool, error) {
	u, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("reading account: %w", err)
	}
	if u.MfaEnabled {
		return true, nil
	}
	n, err := q.CountWebAuthnCredentialsForUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("counting credentials: %w", err)
	}
	return n > 0, nil
}

func (s *Store) Create(ctx context.Context, c webauthn.Credential) error {
	err := s.q.CreateWebAuthnCredential(ctx, dbgen.CreateWebAuthnCredentialParams{
		UserID:         c.UserID,
		CredentialID:   c.CredentialID,
		PublicKey:      c.PublicKey,
		SignCount:      c.SignCount,
		Transports:     c.Transports,
		Aaguid:         c.AAGUID,
		BackupEligible: c.BackupEligible,
		BackupState:    c.BackupState,
		Name:           c.Name,
	})
	if err != nil {
		if isDuplicateCredential(err) {
			return webauthn.ErrCredentialExists
		}
		return fmt.Errorf("registering credential: %w", err)
	}
	return nil
}

// isDuplicateCredential reports whether err is the unique index on
// credential_id refusing an insert.
//
// Matched on the typed Postgres error and the constraint NAME, never on
// err.Error()'s text, for the reason isPolicyInUseViolation gives in
// slastore. The name is checked rather than assumed: a mapping keyed to a
// constraint nobody verified is a mapping that silently never fires, and the
// test alongside this causes the violation to prove it.
func isDuplicateCredential(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "webauthn_credentials_credential_id_key"
}

func (s *Store) ListByUser(ctx context.Context, userID uuid.UUID) ([]webauthn.Credential, error) {
	rows, err := s.q.ListWebAuthnCredentialsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing credentials: %w", err)
	}
	out := make([]webauthn.Credential, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

func (s *Store) GetByCredentialID(ctx context.Context, credentialID []byte) (webauthn.Credential, error) {
	row, err := s.q.GetWebAuthnCredentialByCredentialID(ctx, credentialID)
	if errors.Is(err, sql.ErrNoRows) {
		return webauthn.Credential{}, webauthn.ErrNotFound
	}
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("looking up credential: %w", err)
	}
	return fromRow(row), nil
}

func (s *Store) CountForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	n, err := s.q.CountWebAuthnCredentialsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("counting credentials: %w", err)
	}
	return n, nil
}

func (s *Store) Touch(ctx context.Context, id uuid.UUID, signCount int64) error {
	err := s.q.TouchWebAuthnCredential(ctx, dbgen.TouchWebAuthnCredentialParams{
		ID: id, SignCount: signCount,
	})
	if err != nil {
		return fmt.Errorf("recording credential use: %w", err)
	}
	return nil
}

// Delete removes a credential the given user owns.
//
// "Not yours" and "does not exist" both come back as ErrNotFound, because the
// statement matches on both columns and cannot tell them apart — which is the
// answer we want anyway. Confirming that somebody else's credential id is
// real is a question the caller was not entitled to ask.
func (s *Store) Delete(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.q.DeleteWebAuthnCredential(ctx, dbgen.DeleteWebAuthnCredentialParams{
		ID: id, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return webauthn.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("removing credential: %w", err)
	}
	return nil
}

func fromRow(r dbgen.WebauthnCredential) webauthn.Credential {
	c := webauthn.Credential{
		ID:             r.ID,
		UserID:         r.UserID,
		CredentialID:   r.CredentialID,
		PublicKey:      r.PublicKey,
		SignCount:      r.SignCount,
		Transports:     r.Transports,
		AAGUID:         r.Aaguid,
		BackupEligible: r.BackupEligible,
		BackupState:    r.BackupState,
		Name:           r.Name,
		CreatedAt:      r.CreatedAt,
	}
	if r.LastUsedAt.Valid {
		t := r.LastUsedAt.Time
		c.LastUsedAt = &t
	}
	return c
}

// Compile-time proof this satisfies the domain interface.
var _ webauthn.Store = (*Store)(nil)
