// Package cannedresponsestore implements domain/cannedresponse.Store against PostgreSQL.
package cannedresponsestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
)

// Store implements cannedresponse.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Create(ctx context.Context, cr cannedresponse.CannedResponse) error {
	_, err := s.q.CreateCannedResponse(ctx, dbgen.CreateCannedResponseParams{
		ID:         cr.ID,
		Name:       cr.Name,
		Body:       cr.Body,
		CategoryID: nullUUID(cr.CategoryID),
		TypeID:     nullUUID(cr.TypeID),
		SortOrder:  int32(cr.SortOrder),
	})
	if err != nil {
		return fmt.Errorf("creating canned response: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (cannedresponse.CannedResponse, error) {
	row, err := s.q.GetCannedResponse(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cannedresponse.CannedResponse{}, fmt.Errorf("%w: %s", cannedresponse.ErrNotFound, id)
		}
		return cannedresponse.CannedResponse{}, fmt.Errorf("getting canned response %s: %w", id, err)
	}
	return fromRow(row), nil
}

func (s *Store) Update(ctx context.Context, cr cannedresponse.CannedResponse) error {
	if err := s.q.UpdateCannedResponse(ctx, dbgen.UpdateCannedResponseParams{
		ID:         cr.ID,
		Name:       cr.Name,
		Body:       cr.Body,
		CategoryID: nullUUID(cr.CategoryID),
		TypeID:     nullUUID(cr.TypeID),
		SortOrder:  int32(cr.SortOrder),
	}); err != nil {
		return fmt.Errorf("updating canned response %s: %w", cr.ID, err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteCannedResponse(ctx, id); err != nil {
		return fmt.Errorf("deleting canned response %s: %w", id, err)
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]cannedresponse.CannedResponse, error) {
	rows, err := s.q.ListCannedResponses(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing canned responses: %w", err)
	}
	out := make([]cannedresponse.CannedResponse, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func fromRow(r dbgen.CannedResponse) cannedresponse.CannedResponse {
	return cannedresponse.CannedResponse{
		ID:         r.ID,
		Name:       r.Name,
		Body:       r.Body,
		CategoryID: uuidPtr(r.CategoryID),
		TypeID:     uuidPtr(r.TypeID),
		SortOrder:  int(r.SortOrder),
		CreatedAt:  r.CreatedAt,
	}
}

func nullUUID(p *uuid.UUID) uuid.NullUUID {
	if p == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *p, Valid: true}
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}
