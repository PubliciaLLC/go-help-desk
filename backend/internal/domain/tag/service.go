package tag

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrDeleted is returned when a staff member tries to use a soft-deleted tag.
var ErrDeleted = errors.New("tag has been deleted and can only be restored by an admin")

// ErrNotFound is returned when a tag lookup by name or ID yields no result.
var ErrNotFound = errors.New("tag not found")

// Service encapsulates tag business logic.
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// ListActive returns all non-deleted tags, ordered by name.
func (s *Service) ListActive(ctx context.Context) ([]Tag, error) {
	return s.store.ListActive(ctx)
}

// ListAll returns every tag including deleted ones (admin panel).
func (s *Service) ListAll(ctx context.Context) ([]Tag, error) {
	return s.store.ListAll(ctx)
}

// Search returns active tags whose name starts with prefix (for autocomplete).
func (s *Service) Search(ctx context.Context, prefix string) ([]Tag, error) {
	p := strings.ToLower(strings.TrimSpace(prefix))
	if p == "" {
		return s.store.ListActive(ctx)
	}
	return s.store.Search(ctx, p+"%")
}

// Resolve looks up a tag by name (case-insensitive). If the tag does not exist
// it is created. If the tag exists but is deleted, ErrDeleted is returned so
// the caller can inform the user that only an admin may restore it.
func (s *Service) Resolve(ctx context.Context, rawName string) (Tag, error) {
	name := strings.ToLower(strings.TrimSpace(rawName))
	if name == "" {
		return Tag{}, errors.New("tag name must not be empty")
	}

	existing, err := s.store.GetByName(ctx, name)
	if err == nil {
		// Tag exists — check if it's deleted.
		if existing.DeletedAt != nil {
			return Tag{}, ErrDeleted
		}
		return existing, nil
	}

	// Create new tag.
	return s.store.Create(ctx, name)
}

// Delete soft-deletes a tag. Ticket associations are preserved.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.SoftDelete(ctx, id)
}

// Restore clears the soft-delete on a tag.
func (s *Service) Restore(ctx context.Context, id uuid.UUID) error {
	return s.store.Restore(ctx, id)
}

// ListForTicket returns the tags currently attached to a ticket.
func (s *Service) ListForTicket(ctx context.Context, ticketID uuid.UUID) ([]Tag, error) {
	return s.store.ListForTicket(ctx, ticketID)
}

// AddToTicket attaches a tag to a ticket. If the tag name doesn't exist it is
// created first. Returns ErrDeleted if the tag was soft-deleted.
func (s *Service) AddToTicket(ctx context.Context, ticketID uuid.UUID, rawName string) (Tag, error) {
	tag, err := s.Resolve(ctx, rawName)
	if err != nil {
		return Tag{}, err
	}
	if err := s.store.AddToTicket(ctx, ticketID, tag.ID); err != nil {
		return Tag{}, err
	}
	return tag, nil
}

// RemoveFromTicket detaches a tag from a ticket by tag ID.
func (s *Service) RemoveFromTicket(ctx context.Context, ticketID, tagID uuid.UUID) error {
	return s.store.RemoveFromTicket(ctx, ticketID, tagID)
}
