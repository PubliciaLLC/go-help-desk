// Package adminstore implements domain/admin.Store (settings) against PostgreSQL.
package adminstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Store implements admin.Store and admin.SPPairStore.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil: SetSAMLSPPair runs on q without a transaction of its own
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB is New for a store that also writes a SAML service-provider pair
// with its audit entry in one transaction of its own (SetSAMLSPPair). A Store
// from New runs that on q as it is, which is what a test already inside a
// transaction wants.
func NewWithDB(q *dbgen.Queries, db *sql.DB) *Store { return &Store{q: q, db: db} }

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	row, err := s.q.GetSetting(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("getting setting %q: %w", key, err)
	}
	return row, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	return s.q.SetSetting(ctx, dbgen.SetSettingParams{
		Key:   key,
		Value: json.RawMessage(value),
	})
}

func (s *Store) List(ctx context.Context) (map[string][]byte, error) {
	rows, err := s.q.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing settings: %w", err)
	}
	out := make(map[string][]byte, len(rows))
	for _, r := range rows {
		out[r.Key] = []byte(r.Value)
	}
	return out, nil
}

// errYield rolls a conditional pair write back: somebody stored a certificate
// or a key first.
var errYield = errors.New("an SP certificate or key is already stored")

// SetSAMLSPPair writes the SAML service-provider certificate and key and the
// audit entry that says so in one transaction: the pair is not there without
// its entry, nor the entry without its pair. It reports whether it wrote.
//
// With ifAbsent, each row is written only if it is still empty, and the whole
// write is abandoned (false, no error, no entry) when either is not. The
// conflicting row's lock is the gate (see SetSettingIfEmpty), so two servers
// saving at once cannot both write: the second waits for the first, finds its
// rows filled and yields. Without ifAbsent the pair replaces whatever is there.
func (s *Store) SetSAMLSPPair(ctx context.Context, certPEM, keyPEM string, ifAbsent bool, e audit.Entry) (bool, error) {
	cert, _ := json.Marshal(certPEM)
	key, _ := json.Marshal(keyPEM)
	write := func(q *dbgen.Queries, k string, v []byte) error {
		if !ifAbsent {
			return q.SetSetting(ctx, dbgen.SetSettingParams{Key: k, Value: v})
		}
		n, err := q.SetSettingIfEmpty(ctx, dbgen.SetSettingIfEmptyParams{Key: k, Value: v})
		if err != nil {
			return err
		}
		if n == 0 {
			return errYield
		}
		return nil
	}
	err := s.inTx(ctx, func(q *dbgen.Queries) error {
		// Certificate first, always, so two writers take the same lock first.
		if err := write(q, admin.KeySAMLCertPEM, cert); err != nil {
			return err
		}
		if err := write(q, admin.KeySAMLKeyPEM, key); err != nil {
			return err
		}
		if err := auditstore.New(q).Create(ctx, e); err != nil {
			return fmt.Errorf("writing audit entry: %w", err)
		}
		return nil
	})
	if errors.Is(err, errYield) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storing the SAML SP certificate and key: %w", err)
	}
	return true, nil
}

// inTx runs fn in one transaction, run again when Postgres aborts it to break a
// lock cycle (dbretry has the rules), so fn must not keep state between calls.
func (s *Store) inTx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	if s.db == nil {
		return fn(s.q)
	}
	return dbretry.Do(ctx, nil, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }() // a no-op once committed
		if err := fn(s.q.WithTx(tx)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing: %w", err)
		}
		return nil
	})
}
