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
	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/sqlc-dev/pqtype"
)

// Store implements customfield.Store.
type Store struct {
	q  *dbgen.Queries
	db *sql.DB // nil: SetValues joins the transaction q is already bound to
}

// New returns a Store backed by the given Queries. db is what SetValues opens
// its transaction on. The integration harness passes nil: it already wraps each
// test in a transaction that is rolled back, over the Queries it gives every
// store, and a second transaction on another connection could not see the
// rows that one has written (testutil.JoiningTxRunner is the same trade). A nil
// db therefore does not exercise commit or rollback.
func New(q *dbgen.Queries, db *sql.DB) *Store { return &Store{q: q, db: db} }

// ── Field definitions ──────────────────────────────────────────────────────────

func (s *Store) CreateFieldDef(ctx context.Context, def customfield.FieldDef) (customfield.FieldDef, error) {
	row, err := s.q.CreateCustomFieldDef(ctx, dbgen.CreateCustomFieldDefParams{
		ID:        def.ID,
		Name:      def.Name,
		FieldType: string(def.FieldType),
		Options:   optionsToNullRaw(def.Options),
		SortOrder: int32(def.SortOrder),
		Active:    def.Active,
	})
	if err != nil {
		return customfield.FieldDef{}, mapWriteError("creating field def", err, nameTaken, "")
	}
	return defFromRow(row), nil
}

func (s *Store) ListFieldDefs(ctx context.Context) ([]customfield.FieldDef, error) {
	rows, err := s.q.ListCustomFieldDefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing field defs: %w", err)
	}
	out := make([]customfield.FieldDef, len(rows))
	for i, r := range rows {
		out[i] = defFromRow(r)
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

func (s *Store) UpdateFieldDef(ctx context.Context, def customfield.FieldDef) error {
	err := s.q.UpdateCustomFieldDef(ctx, dbgen.UpdateCustomFieldDefParams{
		ID:        def.ID,
		Name:      def.Name,
		FieldType: string(def.FieldType),
		Options:   optionsToNullRaw(def.Options),
		SortOrder: int32(def.SortOrder),
		Active:    def.Active,
	})
	return mapWriteError("updating field def", err, nameTaken, "")
}

// ── Assignments ───────────────────────────────────────────────────────────────

func (s *Store) CreateAssignment(ctx context.Context, a customfield.Assignment) (customfield.Assignment, error) {
	row, err := s.q.CreateCustomFieldAssignment(ctx, dbgen.CreateCustomFieldAssignmentParams{
		ID:            a.ID,
		FieldDefID:    a.FieldDefID,
		ScopeType:     string(a.ScopeType),
		ScopeID:       a.ScopeID,
		SortOrder:     int32(a.SortOrder),
		VisibleOnNew:  a.VisibleOnNew,
		RequiredOnNew: a.RequiredOnNew,
	})
	if err != nil {
		return customfield.Assignment{}, mapWriteError("creating assignment", err, alreadyAssigned, "field_def_id does not name a field")
	}
	return customfield.Assignment{
		ID:            row.ID,
		FieldDefID:    row.FieldDefID,
		ScopeType:     customfield.ScopeType(row.ScopeType),
		ScopeID:       row.ScopeID,
		SortOrder:     int(row.SortOrder),
		VisibleOnNew:  row.VisibleOnNew,
		RequiredOnNew: row.RequiredOnNew,
	}, nil
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

func (s *Store) GetAssignment(ctx context.Context, id uuid.UUID) (customfield.Assignment, error) {
	row, err := s.q.GetCustomFieldAssignment(ctx, id)
	if err != nil {
		return customfield.Assignment{}, fmt.Errorf("getting assignment %s: %w", id, err)
	}
	return customfield.Assignment{
		ID:            row.ID,
		FieldDefID:    row.FieldDefID,
		ScopeType:     customfield.ScopeType(row.ScopeType),
		ScopeID:       row.ScopeID,
		SortOrder:     int(row.SortOrder),
		VisibleOnNew:  row.VisibleOnNew,
		RequiredOnNew: row.RequiredOnNew,
	}, nil
}

func (s *Store) UpdateAssignment(ctx context.Context, a customfield.Assignment) error {
	return s.q.UpdateCustomFieldAssignment(ctx, dbgen.UpdateCustomFieldAssignmentParams{
		ID:            a.ID,
		SortOrder:     int32(a.SortOrder),
		VisibleOnNew:  a.VisibleOnNew,
		RequiredOnNew: a.RequiredOnNew,
	})
}

func (s *Store) DeleteAssignment(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteCustomFieldAssignment(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting field assignment %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("field assignment %s: %w", id, customfield.ErrNotFound)
	}
	return nil
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

// SetValues locks the named definitions FOR SHARE in id order, lets check
// refuse the batch, and writes every entry in the same transaction. The lock is
// what makes check's answer still true when the rows are written: an admin
// change to a definition takes the row FOR UPDATE and waits for this
// transaction to finish.
func (s *Store) SetValues(ctx context.Context, ticketID uuid.UUID, entries map[uuid.UUID]string, check func(map[uuid.UUID]customfield.FieldDef) error) error {
	if s.db == nil {
		return s.setValues(ctx, s.q, ticketID, entries, check)
	}
	// Run again when Postgres aborts the transaction to break a lock cycle
	// (dbretry has the rules). Each attempt starts clean and check is called
	// afresh.
	return dbretry.Do(ctx, nil, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }() // a no-op once committed
		if err := s.setValues(ctx, s.q.WithTx(tx), ticketID, entries, check); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing field values: %w", err)
		}
		return nil
	})
}

func (s *Store) setValues(ctx context.Context, q *dbgen.Queries, ticketID uuid.UUID, entries map[uuid.UUID]string, check func(map[uuid.UUID]customfield.FieldDef) error) error {
	// Sorted: the lock query returns rows in id order, and writing in the same
	// order keeps two batches that name the same fields from taking the rows of
	// ticket_custom_field_values in opposite orders.
	ids := make([]uuid.UUID, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })

	rows, err := q.LockCustomFieldDefsForShare(ctx, ids)
	if err != nil {
		return fmt.Errorf("locking field defs: %w", err)
	}
	defs := make(map[uuid.UUID]customfield.FieldDef, len(rows))
	for _, r := range rows {
		defs[r.ID] = defFromRow(r)
	}
	if err := check(defs); err != nil {
		return err
	}

	for _, id := range ids {
		if entries[id] == "" {
			if err := q.DeleteCustomFieldValue(ctx, dbgen.DeleteCustomFieldValueParams{TicketID: ticketID, FieldDefID: id}); err != nil {
				return fmt.Errorf("clearing field value: %w", err)
			}
			continue
		}
		err := q.UpsertCustomFieldValue(ctx, dbgen.UpsertCustomFieldValueParams{TicketID: ticketID, FieldDefID: id, Value: entries[id]})
		if err := mapWriteError("saving field value", err, "", "the ticket or the field does not exist"); err != nil {
			return err
		}
	}
	return nil
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
		ID:        r.ID,
		Name:      r.Name,
		FieldType: customfield.FieldType(r.FieldType),
		Options:   optionsFromNullRaw(r.Options),
		SortOrder: int(r.SortOrder),
		Active:    r.Active,
		CreatedAt: r.CreatedAt,
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
