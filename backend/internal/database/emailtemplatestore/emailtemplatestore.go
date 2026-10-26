// Package emailtemplatestore implements domain/emailtemplate.Store against
// PostgreSQL.
package emailtemplatestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
)

// Store implements emailtemplate.Store.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil from New: Apply then joins the caller's transaction
}

// New returns a Store over the given Queries. Apply runs on whatever those
// Queries are bound to, which is how the integration harness keeps a test's
// writes inside its rolled-back transaction.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB returns the production Store: Apply opens a transaction of its own
// so the row and its audit entry commit together.
func NewWithDB(db *sql.DB) *Store { return &Store{q: dbgen.New(db), db: db} }

func fromRow(r dbgen.EmailTemplateOverride) emailtemplate.Override {
	return emailtemplate.Override{
		Name: r.Name, Subject: r.Subject, Body: r.Body, HTML: r.Html.String,
		UpdatedAt: r.UpdatedAt, UpdatedBy: database.UUIDPtr(r.UpdatedBy),
	}
}

// Overrides is one primary-key lookup per name, no cache.
func (s *Store) Overrides(ctx context.Context, names ...string) (map[string]emailtemplate.Override, error) {
	rows, err := s.q.GetEmailTemplateOverrides(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("reading email template overrides: %w", err)
	}
	out := make(map[string]emailtemplate.Override, len(rows))
	for _, r := range rows {
		out[r.Name] = fromRow(r)
	}
	return out, nil
}

func (s *Store) List(ctx context.Context) ([]emailtemplate.Override, error) {
	rows, err := s.q.ListEmailTemplateOverrides(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing email template overrides: %w", err)
	}
	out := make([]emailtemplate.Override, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// Apply sets or removes the override and writes its audit entry in one
// transaction. Run again when Postgres aborts the transaction to break a lock
// cycle (dbretry); every attempt starts clean, and entry is called afresh.
func (s *Store) Apply(ctx context.Context, name string, next *emailtemplate.Override, entry func(prev *emailtemplate.Override) *audit.Entry) error {
	if s.db == nil {
		return s.apply(ctx, s.q, name, next, entry)
	}
	return dbretry.Do(ctx, nil, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }() // a no-op once committed
		if err := s.apply(ctx, s.q.WithTx(tx), name, next, entry); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing email template change: %w", err)
		}
		return nil
	})
}

func (s *Store) apply(ctx context.Context, q *dbgen.Queries, name string, next *emailtemplate.Override, entry func(prev *emailtemplate.Override) *audit.Entry) error {
	// Locked, so the entry's "before" is the row this transaction replaces and
	// not one a concurrent save changed in between.
	var prev *emailtemplate.Override
	row, err := q.LockEmailTemplateOverride(ctx, name)
	switch {
	case err == nil:
		p := fromRow(row)
		prev = &p
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("reading email template override: %w", err)
	}

	if next == nil {
		if err := q.DeleteEmailTemplateOverride(ctx, name); err != nil {
			return fmt.Errorf("removing email template override: %w", err)
		}
	} else if err := q.UpsertEmailTemplateOverride(ctx, dbgen.UpsertEmailTemplateOverrideParams{
		Name: name, Subject: next.Subject, Body: next.Body,
		Html:      sql.NullString{String: next.HTML, Valid: next.HTML != ""},
		UpdatedBy: database.NullUUID(next.UpdatedBy),
	}); err != nil {
		return fmt.Errorf("saving email template override: %w", err)
	}

	if e := entry(prev); e != nil {
		if err := auditstore.New(q).Create(ctx, *e); err != nil {
			return fmt.Errorf("writing audit entry: %w", err)
		}
	}
	return nil
}
