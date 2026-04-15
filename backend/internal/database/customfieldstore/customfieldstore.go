// Package customfieldstore implements domain/customfield.Store against PostgreSQL.
package customfieldstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/sqlc-dev/pqtype"
)

// Store implements customfield.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

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
		return customfield.FieldDef{}, fmt.Errorf("creating field def: %w", err)
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
	if err != nil {
		return customfield.FieldDef{}, fmt.Errorf("getting field def %s: %w", id, err)
	}
	return defFromRow(row), nil
}

func (s *Store) UpdateFieldDef(ctx context.Context, def customfield.FieldDef) error {
	return s.q.UpdateCustomFieldDef(ctx, dbgen.UpdateCustomFieldDefParams{
		ID:        def.ID,
		Name:      def.Name,
		FieldType: string(def.FieldType),
		Options:   optionsToNullRaw(def.Options),
		SortOrder: int32(def.SortOrder),
		Active:    def.Active,
	})
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
		return customfield.Assignment{}, fmt.Errorf("creating assignment: %w", err)
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
	return s.q.DeleteCustomFieldAssignment(ctx, id)
}

// ── Values ────────────────────────────────────────────────────────────────────

func (s *Store) UpsertValue(ctx context.Context, ticketID, fieldDefID uuid.UUID, value string) error {
	return s.q.UpsertCustomFieldValue(ctx, dbgen.UpsertCustomFieldValueParams{
		TicketID:   ticketID,
		FieldDefID: fieldDefID,
		Value:      value,
	})
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

// ── helpers ───────────────────────────────────────────────────────────────────

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
