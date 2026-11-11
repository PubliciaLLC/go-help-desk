// Package auditstore implements domain/audit.Store against PostgreSQL.
package auditstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/sqlc-dev/pqtype"
)

// purgeBatchSize is how many expired entries one DELETE removes. Big enough
// that a purge of millions of rows is hundreds of statements, small enough that
// none holds locks on more than a few thousand rows or writes a large slab of
// WAL at once.
const purgeBatchSize = 5000

// Store implements audit.Store.
type Store struct {
	q          *dbgen.Queries
	purgeBatch int32
}

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store {
	return &Store{q: q, purgeBatch: purgeBatchSize}
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
func (s *Store) Search(ctx context.Context, f audit.Filter, limit, offset int) (audit.Page, error) {
	p, err := searchParams(f)
	if err != nil {
		return audit.Page{}, err
	}
	rows, counted, err := s.search(ctx, p, limit, offset)
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

func (s *Store) search(ctx context.Context, p searchParamValues, limit, offset int) ([]dbgen.AuditLog, int64, error) {
	params := dbgen.SearchAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		QPattern:   p.qPattern,
		PageLimit:  pageInt32(limit + 1),
		PageOffset: pageInt32(offset),

		MaskRequesters: p.maskRequesters,
		SearchDetails:  p.searchDetails,
		DiffPath:       p.diffPath,
		ExcludeActions: p.excludeActions,
	}
	var rows []dbgen.AuditLog
	var err error
	if p.qPattern.Valid {
		// The same statement with an ordering the planner cannot walk the
		// created_at index for; see SearchAuditLogText.
		rows, err = s.q.SearchAuditLogText(ctx, dbgen.SearchAuditLogTextParams(params))
	} else {
		rows, err = s.q.SearchAuditLog(ctx, params)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("searching audit entries: %w", err)
	}
	counted, err := s.q.CountAuditLog(ctx, dbgen.CountAuditLogParams{
		EntityType: p.entityType,
		Action:     p.action,
		ActorID:    p.actorID,
		FromTs:     p.from,
		ToTs:       p.to,
		QPattern:   p.qPattern,
		CountCap:   audit.TotalCap + 1,

		MaskRequesters: p.maskRequesters,
		SearchDetails:  p.searchDetails,
		DiffPath:       p.diffPath,
		ExcludeActions: p.excludeActions,
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

// DeleteOlderThan deletes in batches rather than with one statement, so the
// first purge of a big log (retention is off by default, and turning it on
// meets years of rows at once) does not hold every expired row locked, or make
// one very large transaction. A short batch means nothing is left to take, or
// that someone else holds the rest (SKIP LOCKED): the next sweep gets those.
//
// The total is returned with an error too, because batches already committed
// stay deleted, and the caller logs how many.
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("deleting expired audit entries: %w", err)
		}
		n, err := s.q.DeleteAuditLogBatch(ctx, dbgen.DeleteAuditLogBatchParams{Cutoff: cutoff, BatchSize: s.purgeBatch})
		if err != nil {
			return total, fmt.Errorf("deleting expired audit entries: %w", err)
		}
		total += n
		if n < int64(s.purgeBatch) {
			return total, nil
		}
	}
}

type searchParamValues struct {
	entityType, action sql.NullString
	// qPattern is q escaped for ILIKE; see likeLiteral.
	qPattern sql.NullString
	actorID  uuid.NullUUID
	from, to sql.NullTime
	// diffPath is the jsonpath that looks for q in the shown keys of a diff, see
	// diffPath. nil without a q, which is SQL NULL.
	diffPath       any
	searchDetails  bool
	maskRequesters bool
	// nil, not empty, when nothing is excluded: SQL NULL means "no exclusion".
	excludeActions []string
}

func searchParams(f audit.Filter) (searchParamValues, error) {
	p := searchParamValues{
		entityType: database.NullString(nonEmpty(f.EntityType)),
		action:     database.NullString(nonEmpty(f.Action)),
		actorID:    database.NullUUID(f.ActorID),
		from:       database.NullTime(f.From),
		to:         database.NullTime(f.To),

		maskRequesters: f.MaskRequesterActors,
	}
	if f.Q != "" {
		pattern := likeLiteral(f.Q)
		p.qPattern = database.NullString(&pattern)
		path, err := diffPath(audit.SearchableKeys(), f.Q)
		if err != nil {
			return searchParamValues{}, fmt.Errorf("building the change-details search: %w", err)
		}
		p.diffPath = path
		p.searchDetails = f.SearchesDetails()
	}
	if len(f.ExcludeActions) > 0 {
		p.excludeActions = f.ExcludeActions
	}
	return p, nil
}

// diffPath is the jsonpath that asks whether a diff holds text in one of keys:
//
//	strict $ ? (@."a" like_regex "text" flag "iq" || @."b" like_regex ...)
//
// Each part matters. "q" quotes the pattern, so the text is a literal substring
// and not a regular expression, and "i" ignores case. Strict, because in the
// default lax mode a path unwraps arrays: a shown key holding a list of strings
// would be searched, and so would a diff that is itself a list, and Redact
// shows neither. Strict raises an error there, which a filter reads as "no".
// The text goes in as a JSON string literal, which is what a jsonpath string is,
// so nothing the admin types can end the literal or add a clause.
func diffPath(keys []string, q string) (string, error) {
	lit, err := jsonString(q)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("strict $ ? (")
	for i, k := range keys {
		kl, err := jsonString(k)
		if err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteString(" || ")
		}
		b.WriteString("@." + kl + " like_regex " + lit + ` flag "iq"`)
	}
	b.WriteString(")")
	return b.String(), nil
}

// jsonString is s as a JSON string literal, without Go's HTML escaping.
func jsonString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// likeLiteral makes s match itself, and only itself, as an ILIKE operand:
// "%" and "_" are wildcards and "\" escapes them, so a search for "100%" or
// "disk_full" would otherwise match far more than it says.
func likeLiteral(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
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
		Limit:      pageInt32(limit),
		Offset:     pageInt32(offset),
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

// pageInt32 narrows a page bound to what the generated queries accept.
//
// sqlc emits int32 for LIMIT and OFFSET. Converting a Go int directly wraps
// past MaxInt32: 2147483648 goes negative and Postgres answers "OFFSET must not
// be negative" as a 500, and 4294967296 wraps to zero and silently serves the
// first page. The HTTP handlers bound their own input, but a store takes any
// int from any caller, and the conversion is the place the value stops fitting.
func pageInt32(n int) int32 {
	if n < 0 {
		return 0
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(n)
}
