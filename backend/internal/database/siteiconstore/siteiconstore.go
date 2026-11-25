// Package siteiconstore implements domain/siteicon.Store against PostgreSQL.
package siteiconstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
)

// Store implements siteicon.Store.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil from New: writes then join the caller's transaction
}

// New returns a Store over the given Queries. Writes run on whatever those
// Queries are bound to, which is how the integration harness keeps a test's
// writes inside its rolled-back transaction.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// NewWithDB returns the production Store: each write opens a transaction of its
// own, so the files, the metadata and the audit entry commit together.
func NewWithDB(db *sql.DB) *Store { return &Store{q: dbgen.New(db), db: db} }

// names is every name the store holds, and the only ones it will write.
var names = append([]string{siteicon.Master}, siteicon.Served...)

func (s *Store) Get(ctx context.Context) (siteicon.Icon, bool, error) {
	r, err := s.q.GetSiteIcon(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return siteicon.Icon{}, false, nil
	}
	if err != nil {
		return siteicon.Icon{}, false, fmt.Errorf("reading the site icon: %w", err)
	}
	return siteicon.Icon{
		Background: r.Background, Version: r.Version, SourceSize: int(r.SourceSize),
		SourceSHA256: r.SourceSha256, UpdatedAt: r.UpdatedAt,
	}, true, nil
}

func (s *Store) File(ctx context.Context, name string) ([]byte, string, bool, error) {
	r, err := s.q.GetSiteIconFile(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("reading site icon file %q: %w", name, err)
	}
	return r.Data, r.Version, true, nil
}

func (s *Store) FileSizes(ctx context.Context) (map[string]int, error) {
	rows, err := s.q.ListSiteIconFileSizes(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading site icon file sizes: %w", err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Name] = int(r.Bytes)
	}
	return out, nil
}

// Replace writes the icon, all eight files and the audit entry in one
// transaction. The entry is built from what the lock finds stored, so the
// "before" it records is what this write replaces. (Two first uploads at the
// same instant both find nothing and both record none; the second still
// replaces the first correctly, only its entry says "first".)
func (s *Store) Replace(ctx context.Context, icon siteicon.Icon, files siteicon.Files, entry func(prev *siteicon.Icon) audit.Entry) error {
	if err := checkSet(files); err != nil {
		return err
	}
	return s.inTx(ctx, func(q *dbgen.Queries) error {
		prev, _, err := lock(ctx, q)
		if err != nil {
			return err
		}
		if err := write(ctx, q, icon, files, nil); err != nil {
			return err
		}
		return record(ctx, q, entry(prev))
	})
}

// Update changes the stored icon under the row's lock; see siteicon.Store.
func (s *Store) Update(ctx context.Context, decide func(prev siteicon.Icon, stored siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error)) (bool, error) {
	found := false
	err := s.inTx(ctx, func(q *dbgen.Queries) error {
		found = false
		prev, ok, err := lock(ctx, q)
		if err != nil || !ok {
			return err
		}
		found = true
		stored := make(siteicon.Files, len(names))
		for _, n := range names {
			r, err := q.GetSiteIconFile(ctx, n)
			if err != nil {
				return fmt.Errorf("reading site icon file %q: %w", n, err)
			}
			stored[n] = r.Data
		}
		next, files, e, err := decide(*prev, stored)
		if errors.Is(err, siteicon.ErrUnchanged) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := checkSet(files); err != nil {
			return err
		}
		if err := write(ctx, q, next, files, stored); err != nil {
			return err
		}
		return record(ctx, q, e)
	})
	return found, err
}

// Delete removes the icon (its files go with it, by the foreign key) and writes
// the entry, or writes nothing when there is no icon.
func (s *Store) Delete(ctx context.Context, entry func(prev siteicon.Icon) audit.Entry) (bool, error) {
	deleted := false
	err := s.inTx(ctx, func(q *dbgen.Queries) error {
		deleted = false
		prev, ok, err := lock(ctx, q)
		if err != nil || !ok {
			return err
		}
		if _, err := q.DeleteSiteIcon(ctx); err != nil {
			return fmt.Errorf("removing the site icon: %w", err)
		}
		deleted = true
		return record(ctx, q, entry(*prev))
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// lock takes the icon row's lock and returns what is stored; false when there
// is no row, which locks nothing.
func lock(ctx context.Context, q *dbgen.Queries) (*siteicon.Icon, bool, error) {
	r, err := q.LockSiteIcon(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("locking the site icon: %w", err)
	}
	return &siteicon.Icon{
		Background: r.Background, Version: r.Version, SourceSize: int(r.SourceSize),
		SourceSHA256: r.SourceSha256, UpdatedAt: r.UpdatedAt,
	}, true, nil
}

// write stores the metadata and the files. With old given, a file whose bytes
// did not change is not written again.
func write(ctx context.Context, q *dbgen.Queries, icon siteicon.Icon, files, old siteicon.Files) error {
	if err := q.UpsertSiteIcon(ctx, dbgen.UpsertSiteIconParams{
		Background: icon.Background, Version: icon.Version, SourceSize: int32(icon.SourceSize), SourceSha256: icon.SourceSHA256,
	}); err != nil {
		return fmt.Errorf("saving the site icon: %w", err)
	}
	for _, n := range names {
		if old != nil && bytes.Equal(old[n], files[n]) {
			continue
		}
		if err := q.UpsertSiteIconFile(ctx, dbgen.UpsertSiteIconFileParams{Name: n, Data: files[n]}); err != nil {
			return fmt.Errorf("saving site icon file %q: %w", n, err)
		}
	}
	return nil
}

func record(ctx context.Context, q *dbgen.Queries, e audit.Entry) error {
	if err := auditstore.New(q).Create(ctx, e); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	return nil
}

// checkSet requires exactly the eight files: a partial icon is a broken one,
// and the table's CHECK would refuse a stranger only after the rest were written.
func checkSet(files siteicon.Files) error {
	if len(files) != len(names) {
		return fmt.Errorf("a site icon is %d files, not %d", len(names), len(files))
	}
	for _, n := range names {
		if len(files[n]) == 0 {
			return fmt.Errorf("the site icon has no %s", n)
		}
	}
	return nil
}

// inTx runs fn in a transaction of its own, again if Postgres aborts it to break
// a lock cycle; or, from New, on the caller's Queries as they are.
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
			return fmt.Errorf("committing the site icon change: %w", err)
		}
		return nil
	})
}
