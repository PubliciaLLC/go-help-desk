// Package auditstore implements domain/audit.Store against PostgreSQL.
package auditstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/sqlc-dev/pqtype"
)

// txBeginner is implemented by *sql.DB and allows tests to spy on transaction options.
type txBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// Store implements audit.Store.
type Store struct {
	q    *dbgen.Queries
	snap txBeginner // optional; if set, scoped searches run in a repeatable-read snapshot transaction
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

// SnapshotOn returns a copy of the Store with snapshot transactions enabled.
// When set, scoped searches (Filter.ScopedTo != nil) run GetAuditTicketScope,
// SearchAuditLogScoped, and CountAuditLogScoped within a single repeatable-read
// transaction, preventing membership changes from mixing old scope with new
// ticket state in one request.
func (s *Store) SnapshotOn(db txBeginner) *Store {
	return &Store{
		q:    s.q,
		snap: db,
	}
}

func (s *Store) Create(ctx context.Context, e audit.Entry) error {
	before, _ := marshalMap(e.Before)
	after, _ := marshalMap(e.After)
	return s.q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
		ID:         e.ID,
		ActorID:    database.NullUUID(e.ActorID),
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Action:     e.Action,
		Before:     before,
		After:      after,
		CreatedAt:  time.Now(),
	})
}

// Search reads the page and counts the matches, both from the same predicate.
// The page is fetched one row long: the extra row is how HasMore is known
// without consulting the count, which stops at audit.TotalCap. The count is
// asked for TotalCap+1 matches, so "more than the cap" is told apart from
// "exactly the cap" without counting any further.
//
// A scoped request (f.ScopedTo set) runs different statements, not the same
// ones with a flag: the staff member's groups and rules are read first and
// passed in as values, because the right plan depends on how many tickets
// they reach (#331, and the note above CountAuditLogScoped). The page and the
// count are given the same scope, so they describe one sequence.
func (s *Store) Search(ctx context.Context, f audit.Filter, limit, offset int) (audit.Page, error) {
	var (
		rows    []dbgen.AuditLog
		counted int64
		err     error
	)
	if f.ScopedTo == nil {
		rows, counted, err = s.searchUnscoped(ctx, searchParams(f), limit, offset)
	} else {
		rows, counted, err = s.searchScoped(ctx, searchParams(f), *f.ScopedTo, limit, offset)
	}
	if err != nil {
		return audit.Page{}, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	capped := counted > audit.TotalCap
	total := int(counted)
	if capped {
		total = audit.TotalCap
	}
	return audit.Page{Entries: toEntries(rows), Total: total, TotalCapped: capped, HasMore: hasMore}, nil
}

func (s *Store) searchUnscoped(ctx context.Context, p searchParamValues, limit, offset int) ([]dbgen.AuditLog, int64, error) {
	rows, err := s.q.SearchAuditLog(ctx, dbgen.SearchAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		Q:          p.q,
		PageLimit:  int32(limit + 1),
		PageOffset: int32(offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("searching audit entries: %w", err)
	}
	counted, err := s.q.CountAuditLog(ctx, dbgen.CountAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		Q:          p.q,
		CountCap:   audit.TotalCap + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("counting audit entries: %w", err)
	}
	return rows, counted, nil
}

func (s *Store) searchScoped(ctx context.Context, p searchParamValues, userID uuid.UUID, limit, offset int) ([]dbgen.AuditLog, int64, error) {
	// If snap is set, run the three reads in a single repeatable-read transaction.
	// This prevents membership changes between calls from mixing old scope with new
	// ticket state in one request. If snap is nil, the caller's Queries are already
	// bound to a transaction (e.g., in tests using testutil.TxQueries), so use them
	// directly.
	q := s.q
	if s.snap != nil {
		tx, err := s.snap.BeginTx(ctx, &sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("beginning audit snapshot: %w", err)
		}
		defer func() {
			// Read-only: nothing to commit, so rollback is the release.
			_ = tx.Rollback()
		}()
		q = s.q.WithTx(tx)
	}

	sc, err := q.GetAuditTicketScope(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("reading ticket scope for audit search: %w", err)
	}
	rows, err := q.SearchAuditLogScoped(ctx, dbgen.SearchAuditLogScopedParams{
		EntityType:       p.entityType,
		Action:           p.action,
		ActorID:          p.actorID,
		FromTs:           p.from,
		ToTs:             p.to,
		Q:                p.q,
		UserID:           userID,
		GroupIds:         sc.GroupIds,
		CategoryIds:      sc.CategoryIds,
		TypedCategoryIds: sc.TypedCategoryIds,
		TypeIds:          sc.TypeIds,
		PageLimit:        int32(limit + 1),
		PageOffset:       int32(offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("searching audit entries: %w", err)
	}
	counted, err := q.CountAuditLogScoped(ctx, dbgen.CountAuditLogScopedParams{
		EntityType:       p.entityType,
		Action:           p.action,
		ActorID:          p.actorID,
		FromTs:           p.from,
		ToTs:             p.to,
		Q:                p.q,
		UserID:           userID,
		GroupIds:         sc.GroupIds,
		CategoryIds:      sc.CategoryIds,
		TypedCategoryIds: sc.TypedCategoryIds,
		TypeIds:          sc.TypeIds,
		CountCap:         audit.TotalCap + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("counting audit entries: %w", err)
	}
	return rows, counted, nil
}

func toEntries(rows []dbgen.AuditLog) []audit.Entry {
	out := make([]audit.Entry, len(rows))
	for i, r := range rows {
		out[i] = audit.Entry{
			ID:         r.ID,
			ActorID:    database.UUIDPtr(r.ActorID),
			EntityType: r.EntityType,
			EntityID:   r.EntityID,
			Action:     r.Action,
			Before:     unmarshalMap(r.Before),
			After:      unmarshalMap(r.After),
			CreatedAt:  r.CreatedAt,
		}
	}
	return out
}

func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteAuditLogBefore(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("deleting expired audit entries: %w", err)
	}
	return n, nil
}

type searchParamValues struct {
	entityType, action, q sql.NullString
	actorID               uuid.NullUUID
	from, to              sql.NullTime
}

func searchParams(f audit.Filter) searchParamValues {
	return searchParamValues{
		entityType: database.NullString(nonEmpty(f.EntityType)),
		action:     database.NullString(nonEmpty(f.Action)),
		q:          database.NullString(nonEmpty(f.Q)),
		actorID:    database.NullUUID(f.ActorID),
		from:       database.NullTime(f.From),
		to:         database.NullTime(f.To),
	}
}

// nonEmpty turns an empty filter string into a nil pointer, so an unset
// EntityType/Action/Q reaches the query as SQL NULL — "match anything" —
// rather than as the empty string, which would only ever match a row that
// literally has one.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Store) ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]audit.Entry, error) {
	rows, err := s.q.ListAuditByEntity(ctx, dbgen.ListAuditByEntityParams{
		EntityType: entityType,
		EntityID:   entityID,
		Limit:      int32(limit),
		Offset:     int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("listing audit entries: %w", err)
	}
	out := make([]audit.Entry, len(rows))
	for i, r := range rows {
		out[i] = audit.Entry{
			ID:         r.ID,
			ActorID:    database.UUIDPtr(r.ActorID),
			EntityType: r.EntityType,
			EntityID:   r.EntityID,
			Action:     r.Action,
			Before:     unmarshalMap(r.Before),
			After:      unmarshalMap(r.After),
			CreatedAt:  r.CreatedAt,
		}
	}
	return out, nil
}

func marshalMap(m map[string]any) (pqtype.NullRawMessage, error) {
	if m == nil {
		return pqtype.NullRawMessage{}, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return pqtype.NullRawMessage{}, err
	}
	return pqtype.NullRawMessage{RawMessage: b, Valid: true}, nil
}

func unmarshalMap(n pqtype.NullRawMessage) map[string]any {
	if !n.Valid {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(n.RawMessage, &m)
	return m
}
