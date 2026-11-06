// Package customfieldstore implements domain/customfield.Store against PostgreSQL.
package customfieldstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/sqlc-dev/pqtype"
)

// Store implements customfield.Store.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil: writes join the transaction q is already bound to
}

// New returns a Store backed by the given Queries. db is what the writes open
// their transactions on. The integration harness passes nil: it already wraps each
// test in a transaction that is rolled back, over the Queries it gives every
// store, and a second transaction on another connection could not see the
// rows that one has written (testutil.JoiningTxRunner is the same trade). A nil
// db therefore does not exercise commit or rollback.
func New(q *dbgen.Queries, db *sql.DB) *Store { return &Store{q: q, db: db} }

// inTx runs fn in one transaction: its statements and the audit entries it
// writes commit together or not at all. Run again when Postgres aborts the
// transaction to break a lock cycle (dbretry has the rules); every attempt
// starts clean, so fn must not keep state between calls.
func (s *Store) inTx(ctx context.Context, what string, fn func(q *dbgen.Queries) error) error {
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
			return fmt.Errorf("committing %s: %w", what, err)
		}
		return nil
	})
}

// writeEntries puts the audit entries in the caller's transaction. A failure is
// returned, and rolls the change back with it.
func writeEntries(ctx context.Context, q *dbgen.Queries, entries ...audit.Entry) error {
	au := auditstore.New(q)
	for _, e := range entries {
		if err := au.Create(ctx, e); err != nil {
			return fmt.Errorf("writing audit entry: %w", err)
		}
	}
	return nil
}

// ── Field definitions ──────────────────────────────────────────────────────────

func (s *Store) CreateFieldDef(ctx context.Context, def customfield.FieldDef, entries ...audit.Entry) (customfield.FieldDef, error) {
	var out customfield.FieldDef
	err := s.inTx(ctx, "field definition", func(q *dbgen.Queries) error {
		row, err := q.CreateCustomFieldDef(ctx, dbgen.CreateCustomFieldDefParams{
			ID:             def.ID,
			Name:           def.Name,
			FieldType:      string(def.FieldType),
			Options:        optionsToNullRaw(def.Options),
			RetiredOptions: optionsToNullRaw(def.RetiredOptions),
			SortOrder:      int32(def.SortOrder),
			Active:         def.Active,
		})
		if err != nil {
			return mapWriteError("creating field def", err, nameTaken, "")
		}
		out = defFromRow(row)
		return writeEntries(ctx, q, entries...)
	})
	if err != nil {
		return customfield.FieldDef{}, err
	}
	return out, nil
}

func (s *Store) ListFieldDefs(ctx context.Context) ([]customfield.FieldDef, error) {
	rows, err := s.q.ListCustomFieldDefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing field defs: %w", err)
	}
	out := make([]customfield.FieldDef, len(rows))
	for i, r := range rows {
		out[i] = defFromRow(dbgen.CustomFieldDef{
			ID: r.ID, Name: r.Name, FieldType: r.FieldType, Options: r.Options, SortOrder: r.SortOrder,
			Active: r.Active, CreatedAt: r.CreatedAt, RetiredOptions: r.RetiredOptions,
		})
		places := int(r.PlaceCount)
		out[i].PlaceCount = &places
	}
	return out, nil
}

func (s *Store) GetFieldDef(ctx context.Context, id uuid.UUID) (customfield.FieldDef, error) {
	row, err := s.q.GetCustomFieldDef(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return customfield.FieldDef{}, fmt.Errorf("field def %s: %w", id, customfield.ErrNotFound)
	}
	if err != nil {
		return customfield.FieldDef{}, fmt.Errorf("getting field def %s: %w", id, err)
	}
	return defFromRow(row), nil
}

// UpdateFieldDef takes the definition's row FOR UPDATE, which waits for every
// value write holding it FOR SHARE (SetValues) and holds off every one that
// starts after, until this commits. decide's answers about what tickets hold
// (held) are therefore still true when the change is written.
func (s *Store) UpdateFieldDef(ctx context.Context, id uuid.UUID, decide func(cur customfield.FieldDef, held customfield.Held) (customfield.FieldDef, []audit.Entry, error)) (customfield.FieldDef, error) {
	var saved customfield.FieldDef
	err := s.inTx(ctx, "field definition change", func(q *dbgen.Queries) error {
		row, err := q.LockCustomFieldDefForUpdate(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("field def %s: %w", id, customfield.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("locking field def %s: %w", id, err)
		}
		next, entries, err := decide(defFromRow(row), held{ctx: ctx, q: q, id: id})
		if err != nil {
			return err
		}
		err = q.UpdateCustomFieldDef(ctx, dbgen.UpdateCustomFieldDefParams{
			ID:             id,
			Name:           next.Name,
			FieldType:      string(next.FieldType),
			Options:        optionsToNullRaw(next.Options),
			RetiredOptions: optionsToNullRaw(next.RetiredOptions),
			SortOrder:      int32(next.SortOrder),
			Active:         next.Active,
		})
		if err := mapWriteError("updating field def", err, nameTaken, ""); err != nil {
			return err
		}
		if err := writeEntries(ctx, q, entries...); err != nil {
			return err
		}
		// The row as stored, not the edited copy.
		after, err := q.GetCustomFieldDef(ctx, id)
		if err != nil {
			return fmt.Errorf("reading field def %s back: %w", id, err)
		}
		saved = defFromRow(after)
		return nil
	})
	if err != nil {
		return customfield.FieldDef{}, err
	}
	return saved, nil
}

// ReorderFieldDefs locks every definition, in id order (the order SetValues
// takes the ones it names), so no field is added, changed or written to while
// the numbers are rewritten.
func (s *Store) ReorderFieldDefs(ctx context.Context, decide func(cur []customfield.FieldDef) ([]uuid.UUID, []audit.Entry, error)) error {
	return s.inTx(ctx, "field order", func(q *dbgen.Queries) error {
		rows, err := q.LockAllCustomFieldDefsForUpdate(ctx)
		if err != nil {
			return fmt.Errorf("locking field defs: %w", err)
		}
		cur := make([]customfield.FieldDef, len(rows))
		for i, r := range rows {
			cur[i] = defFromRow(r)
		}
		ids, entries, err := decide(cur)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := q.ReorderCustomFieldDefs(ctx, ids); err != nil {
				return fmt.Errorf("reordering field defs: %w", err)
			}
		}
		return writeEntries(ctx, q, entries...)
	})
}

// held answers customfield.Held on the transaction the definition is locked in.
type held struct {
	ctx context.Context
	q   *dbgen.Queries
	id  uuid.UUID
}

func (h held) Any() (bool, error) { return h.q.AnyCustomFieldValue(h.ctx, h.id) }

func (h held) OneOf(values []string) (string, bool, error) {
	v, err := h.q.FirstHeldCustomFieldValue(h.ctx, dbgen.FirstHeldCustomFieldValueParams{FieldDefID: h.id, Candidates: values})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// ── Usage ─────────────────────────────────────────────────────────────────────

func (s *Store) CountValues(ctx context.Context, fieldDefID uuid.UUID) (int, error) {
	n, err := s.q.CountCustomFieldValues(ctx, fieldDefID)
	if err != nil {
		return 0, fmt.Errorf("counting values of field %s: %w", fieldDefID, err)
	}
	return int(n), nil
}

func (s *Store) CountValuesByValue(ctx context.Context, fieldDefID uuid.UUID) (map[string]int, error) {
	rows, err := s.q.CountCustomFieldValuesByValue(ctx, fieldDefID)
	if err != nil {
		return nil, fmt.Errorf("counting values of field %s by value: %w", fieldDefID, err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Value] = int(r.Tickets)
	}
	return out, nil
}

// ── Assignments ───────────────────────────────────────────────────────────────

func (s *Store) CreateAssignment(ctx context.Context, a customfield.Assignment, entry ...func(customfield.AssignmentInfo) audit.Entry) (customfield.Assignment, error) {
	var out customfield.Assignment
	err := s.inTx(ctx, "field attachment", func(q *dbgen.Queries) error {
		row, err := q.CreateCustomFieldAssignment(ctx, dbgen.CreateCustomFieldAssignmentParams{
			ID:            a.ID,
			FieldDefID:    a.FieldDefID,
			ScopeType:     string(a.ScopeType),
			ScopeID:       a.ScopeID,
			SortOrder:     int32(a.SortOrder),
			VisibleOnNew:  a.VisibleOnNew,
			RequiredOnNew: a.RequiredOnNew,
		})
		if err != nil {
			return mapWriteError("creating assignment", err, alreadyAssigned, "field_def_id does not name a field")
		}
		out = assignmentFromRow(row)
		if len(entry) == 0 {
			return nil
		}
		info, err := assignmentInfo(ctx, q, out)
		if err != nil {
			return err
		}
		for _, build := range entry {
			if err := writeEntries(ctx, q, build(info)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return customfield.Assignment{}, err
	}
	return out, nil
}

func (s *Store) ListAssignments(ctx context.Context, scopeType customfield.ScopeType, scopeID uuid.UUID) ([]customfield.Assignment, error) {
	rows, err := s.q.ListAssignmentsForScope(ctx, dbgen.ListAssignmentsForScopeParams{
		ScopeType: string(scopeType),
		ScopeID:   scopeID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing assignments for scope: %w", err)
	}
	out := make([]customfield.Assignment, len(rows))
	for i, r := range rows {
		opts := optionsFromNullRaw(r.FieldOptions)
		fd := &customfield.FieldDef{
			ID:        r.FieldDefID,
			Name:      r.FieldName,
			FieldType: customfield.FieldType(r.FieldType),
			Options:   opts,
			SortOrder: int(r.FieldSortOrder),
			Active:    r.FieldActive,
		}
		out[i] = customfield.Assignment{
			ID:            r.ID,
			FieldDefID:    r.FieldDefID,
			FieldDef:      fd,
			ScopeType:     customfield.ScopeType(r.ScopeType),
			ScopeID:       r.ScopeID,
			SortOrder:     int(r.SortOrder),
			VisibleOnNew:  r.VisibleOnNew,
			RequiredOnNew: r.RequiredOnNew,
		}
	}
	return out, nil
}

func (s *Store) ListPlacements(ctx context.Context, fieldDefID uuid.UUID) ([]customfield.Placement, error) {
	rows, err := s.q.ListAssignmentsForFieldDef(ctx, fieldDefID)
	if err != nil {
		return nil, fmt.Errorf("listing placements of field %s: %w", fieldDefID, err)
	}
	out := make([]customfield.Placement, len(rows))
	for i, r := range rows {
		p := customfield.Placement{
			Assignment: customfield.Assignment{
				ID: r.ID, FieldDefID: r.FieldDefID, ScopeType: customfield.ScopeType(r.ScopeType), ScopeID: r.ScopeID,
				SortOrder: int(r.SortOrder), VisibleOnNew: r.VisibleOnNew, RequiredOnNew: r.RequiredOnNew,
			},
			Category: customfield.PlaceNode{ID: r.CategoryID, Name: r.CategoryName, Active: r.CategoryActive},
		}
		if r.TypeID.Valid {
			p.Type = &customfield.PlaceNode{ID: r.TypeID.UUID, Name: r.TypeName.String, Active: r.TypeActive.Bool}
		}
		if r.ItemID.Valid {
			p.Item = &customfield.PlaceNode{ID: r.ItemID.UUID, Name: r.ItemName.String, Active: r.ItemActive.Bool}
		}
		out[i] = p
	}
	return out, nil
}

func (s *Store) GetAssignment(ctx context.Context, id uuid.UUID) (customfield.Assignment, error) {
	row, err := s.q.GetCustomFieldAssignment(ctx, id)
	if err != nil {
		return customfield.Assignment{}, fmt.Errorf("getting assignment %s: %w", id, err)
	}
	return assignmentFromRow(row), nil
}

func (s *Store) UpdateAssignment(ctx context.Context, a customfield.Assignment, entries func(before customfield.Assignment, info customfield.AssignmentInfo) []audit.Entry) error {
	return s.inTx(ctx, "field attachment change", func(q *dbgen.Queries) error {
		before, err := lockAssignment(ctx, q, a.ID)
		if err != nil {
			return err
		}
		if err := q.UpdateCustomFieldAssignment(ctx, dbgen.UpdateCustomFieldAssignmentParams{
			ID:            a.ID,
			SortOrder:     int32(a.SortOrder),
			VisibleOnNew:  a.VisibleOnNew,
			RequiredOnNew: a.RequiredOnNew,
		}); err != nil {
			return fmt.Errorf("updating assignment %s: %w", a.ID, err)
		}
		info, err := assignmentInfo(ctx, q, before)
		if err != nil {
			return err
		}
		return writeEntries(ctx, q, entries(before, info)...)
	})
}

func (s *Store) DeleteAssignment(ctx context.Context, id uuid.UUID, entry func(before customfield.Assignment, info customfield.AssignmentInfo) audit.Entry) error {
	return s.inTx(ctx, "field detachment", func(q *dbgen.Queries) error {
		before, err := lockAssignment(ctx, q, id)
		if err != nil {
			return err
		}
		info, err := assignmentInfo(ctx, q, before)
		if err != nil {
			return err
		}
		n, err := q.DeleteCustomFieldAssignment(ctx, id)
		if err != nil {
			return fmt.Errorf("deleting field assignment %s: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("field assignment %s: %w", id, customfield.ErrNotFound)
		}
		return writeEntries(ctx, q, entry(before, info))
	})
}

// lockAssignment reads the row FOR UPDATE, so the entry's "before" is what this
// transaction then changes.
func lockAssignment(ctx context.Context, q *dbgen.Queries, id uuid.UUID) (customfield.Assignment, error) {
	row, err := q.GetCustomFieldAssignmentForUpdate(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return customfield.Assignment{}, fmt.Errorf("field assignment %s: %w", id, customfield.ErrNotFound)
	}
	if err != nil {
		return customfield.Assignment{}, fmt.Errorf("locking field assignment %s: %w", id, err)
	}
	return assignmentFromRow(row), nil
}

// assignmentInfo is the field's name and the node's path as they stand in this
// transaction, for the audit entry. The path is empty if the node is gone, which
// the routes do not let happen: they check the whole path first.
func assignmentInfo(ctx context.Context, q *dbgen.Queries, a customfield.Assignment) (customfield.AssignmentInfo, error) {
	def, err := q.GetCustomFieldDef(ctx, a.FieldDefID)
	if err != nil {
		return customfield.AssignmentInfo{}, fmt.Errorf("reading field %s for its audit entry: %w", a.FieldDefID, err)
	}
	path, err := q.GetAssignmentScopePath(ctx, dbgen.GetAssignmentScopePathParams{ScopeType: string(a.ScopeType), ScopeID: a.ScopeID})
	if err != nil {
		return customfield.AssignmentInfo{}, fmt.Errorf("reading the path of %s %s for its audit entry: %w", a.ScopeType, a.ScopeID, err)
	}
	return customfield.AssignmentInfo{FieldName: def.Name, ScopePath: path}, nil
}

// ── Values ────────────────────────────────────────────────────────────────────

func (s *Store) UpsertValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error {
	err := s.q.UpsertCustomFieldValue(ctx, dbgen.UpsertCustomFieldValueParams{
		TicketID:   ticketID,
		FieldDefID: fieldDefID,
		Value:      value,
	})
	return mapWriteError("saving field value", err, "", "the ticket or the field does not exist")
}

func (s *Store) DeleteValue(ctx context.Context, ticketID, fieldDefID uuid.UUID) error {
	return s.q.DeleteCustomFieldValue(ctx, dbgen.DeleteCustomFieldValueParams{
		TicketID:   ticketID,
		FieldDefID: fieldDefID,
	})
}

func (s *Store) ListValuesForTicket(ctx context.Context, ticketID uuid.UUID) ([]customfield.TicketFieldValue, error) {
	rows, err := s.q.ListCustomFieldValuesForTicket(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing field values for ticket: %w", err)
	}
	out := make([]customfield.TicketFieldValue, len(rows))
	for i, r := range rows {
		out[i] = customfield.TicketFieldValue{
			TicketID:   r.TicketID,
			FieldDefID: r.FieldDefID,
			FieldName:  r.FieldName,
			FieldType:  customfield.FieldType(r.FieldType),
			Options:    optionsFromNullRaw(r.FieldOptions),
			Value:      r.Value,
			UpdatedAt:  r.UpdatedAt,
		}
	}
	return out, nil
}

// SetValues locks the named definitions FOR SHARE in id order, lets decide
// refuse or trim the batch, and writes what it returns in the same transaction.
// The lock is what makes decide's answer still true when the rows are written:
// an admin change to a definition (UpdateFieldDef) takes the row FOR UPDATE and
// waits for this transaction to finish.
func (s *Store) SetValues(ctx context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, decide func(map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error)) error {
	return s.inTx(ctx, "field values", func(q *dbgen.Queries) error {
		return s.setValues(ctx, q, ticketID, entries, decide)
	})
}

func (s *Store) setValues(ctx context.Context, q *dbgen.Queries, ticketID uuid.UUID, entries map[uuid.UUID]string, decide func(map[uuid.UUID]customfield.FieldDef) (map[uuid.UUID]string, error)) error {
	ids := sortedIDs(entries)
	rows, err := q.LockCustomFieldDefsForShare(ctx, ids)
	if err != nil {
		return fmt.Errorf("locking field defs: %w", err)
	}
	defs := make(map[uuid.UUID]customfield.FieldDef, len(rows))
	for _, r := range rows {
		defs[r.ID] = defFromRow(r)
	}
	write, err := decide(defs)
	if err != nil {
		return err
	}

	// In id order: two batches that name the same fields then take the rows of
	// ticket_custom_field_values in the same order, and cannot deadlock.
	for _, id := range sortedIDs(write) {
		if write[id] == "" {
			if err := q.DeleteCustomFieldValue(ctx, dbgen.DeleteCustomFieldValueParams{TicketID: ticketID, FieldDefID: id}); err != nil {
				return fmt.Errorf("clearing field value: %w", err)
			}
			continue
		}
		err := q.UpsertCustomFieldValue(ctx, dbgen.UpsertCustomFieldValueParams{TicketID: ticketID, FieldDefID: id, Value: write[id]})
		if err := mapWriteError("saving field value", err, "", "the ticket or the field does not exist"); err != nil {
			return err
		}
	}
	return nil
}

func sortedIDs[V any](m map[uuid.UUID]V) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}

// ── helpers ───────────────────────────────────────────────────────────────────

const (
	nameTaken       = "a custom field with this name already exists"
	alreadyAssigned = "this field is already assigned here"
)

// mapWriteError turns the two constraint failures a caller can cause into
// errors that carry a message written here. Detected from the SQLSTATE, never
// from the driver's text, and that text is not passed on: it names the
// constraint and the table. Anything else is wrapped as it always was and
// reaches the handler as a 500.
func mapWriteError(op string, err error, conflictMsg, refMsg string) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && conflictMsg != "":
			return customfield.Conflict(conflictMsg)
		case pgErr.Code == "23503" && refMsg != "":
			return customfield.InvalidReference(refMsg)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

func defFromRow(r dbgen.CustomFieldDef) customfield.FieldDef {
	return customfield.FieldDef{
		ID:             r.ID,
		Name:           r.Name,
		FieldType:      customfield.FieldType(r.FieldType),
		Options:        optionsFromNullRaw(r.Options),
		RetiredOptions: optionsFromNullRaw(r.RetiredOptions),
		SortOrder:      int(r.SortOrder),
		Active:         r.Active,
		CreatedAt:      r.CreatedAt,
	}
}

func assignmentFromRow(r dbgen.CustomFieldAssignment) customfield.Assignment {
	return customfield.Assignment{
		ID:            r.ID,
		FieldDefID:    r.FieldDefID,
		ScopeType:     customfield.ScopeType(r.ScopeType),
		ScopeID:       r.ScopeID,
		SortOrder:     int(r.SortOrder),
		VisibleOnNew:  r.VisibleOnNew,
		RequiredOnNew: r.RequiredOnNew,
	}
}

func optionsToNullRaw(opts []string) pqtype.NullRawMessage {
	if len(opts) == 0 {
		return pqtype.NullRawMessage{}
	}
	b, _ := json.Marshal(opts)
	return pqtype.NullRawMessage{RawMessage: b, Valid: true}
}

func optionsFromNullRaw(msg pqtype.NullRawMessage) []string {
	if !msg.Valid || len(msg.RawMessage) == 0 {
		return nil
	}
	var opts []string
	if err := json.Unmarshal(msg.RawMessage, &opts); err != nil {
		return nil
	}
	return opts
}
