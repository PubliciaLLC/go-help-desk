// Package tagstore implements domain/tag.Store against PostgreSQL.
package tagstore

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/dbgen"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/tag"
)

// Store implements tag.Store.
type Store struct{ q *dbgen.Queries }

func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func toTag(r dbgen.Tag) tag.Tag {
	t := tag.Tag{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt}
	if r.DeletedAt.Valid {
		v := r.DeletedAt.Time
		t.DeletedAt = &v
	}
	return t
}

func (s *Store) Create(ctx context.Context, name string) (tag.Tag, error) {
	r, err := s.q.CreateTag(ctx, name)
	if err != nil {
		return tag.Tag{}, fmt.Errorf("creating tag %q: %w", name, err)
	}
	return toTag(r), nil
}

func (s *Store) GetByName(ctx context.Context, name string) (tag.Tag, error) {
	r, err := s.q.GetTagByName(ctx, name)
	if err != nil {
		return tag.Tag{}, fmt.Errorf("getting tag %q: %w", name, err)
	}
	return toTag(r), nil
}

func (s *Store) ListActive(ctx context.Context) ([]tag.Tag, error) {
	rows, err := s.q.ListActiveTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing active tags: %w", err)
	}
	out := make([]tag.Tag, len(rows))
	for i, r := range rows {
		out[i] = toTag(r)
	}
	return out, nil
}

func (s *Store) ListAll(ctx context.Context) ([]tag.Tag, error) {
	rows, err := s.q.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing all tags: %w", err)
	}
	out := make([]tag.Tag, len(rows))
	for i, r := range rows {
		out[i] = toTag(r)
	}
	return out, nil
}

func (s *Store) Search(ctx context.Context, likePattern string) ([]tag.Tag, error) {
	rows, err := s.q.SearchActiveTags(ctx, likePattern)
	if err != nil {
		return nil, fmt.Errorf("searching tags: %w", err)
	}
	out := make([]tag.Tag, len(rows))
	for i, r := range rows {
		out[i] = toTag(r)
	}
	return out, nil
}

func (s *Store) SoftDelete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteTag(ctx, id)
}

func (s *Store) Restore(ctx context.Context, id uuid.UUID) error {
	return s.q.RestoreTag(ctx, id)
}

func (s *Store) ListForTicket(ctx context.Context, ticketID uuid.UUID) ([]tag.Tag, error) {
	rows, err := s.q.ListTicketTags(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("listing ticket tags: %w", err)
	}
	out := make([]tag.Tag, len(rows))
	for i, r := range rows {
		out[i] = toTag(r)
	}
	return out, nil
}

func (s *Store) AddToTicket(ctx context.Context, ticketID, tagID uuid.UUID) error {
	return s.q.AddTicketTag(ctx, dbgen.AddTicketTagParams{
		TicketID: ticketID,
		TagID:    tagID,
	})
}

func (s *Store) RemoveFromTicket(ctx context.Context, ticketID, tagID uuid.UUID) error {
	return s.q.RemoveTicketTag(ctx, dbgen.RemoveTicketTagParams{
		TicketID: ticketID,
		TagID:    tagID,
	})
}
