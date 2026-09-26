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

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
)

// Store implements webauthn.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

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
