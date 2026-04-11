package group

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Service manages groups, memberships, and scopes.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Create(ctx context.Context, name, description string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, fmt.Errorf("group name is required")
	}
	g := Group{ID: uuid.New(), Name: name, Description: description}
	if err := s.store.Create(ctx, g); err != nil {
		return Group{}, fmt.Errorf("creating group: %w", err)
	}
	return g, nil
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (Group, error) {
	return s.store.GetByID(ctx, id)
}

func (s *Service) Update(ctx context.Context, g Group) error {
	if strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("group name is required")
	}
	return s.store.Update(ctx, g)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.Delete(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Group, error) {
	return s.store.List(ctx)
}

func (s *Service) AddMember(ctx context.Context, groupID, userID uuid.UUID) error {
	return s.store.AddMember(ctx, groupID, userID)
}

func (s *Service) RemoveMember(ctx context.Context, groupID, userID uuid.UUID) error {
	return s.store.RemoveMember(ctx, groupID, userID)
}

func (s *Service) ListMembers(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.ListMembers(ctx, groupID)
}

func (s *Service) ListGroupsForUser(ctx context.Context, userID uuid.UUID) ([]Group, error) {
	return s.store.ListGroupsForUser(ctx, userID)
}

func (s *Service) AddScope(ctx context.Context, sc GroupScope) error {
	return s.store.AddScope(ctx, sc)
}

func (s *Service) RemoveScope(ctx context.Context, groupID, categoryID uuid.UUID, typeID *uuid.UUID) error {
	return s.store.RemoveScope(ctx, groupID, categoryID, typeID)
}

func (s *Service) ListScopes(ctx context.Context, groupID uuid.UUID) ([]GroupScope, error) {
	return s.store.ListScopes(ctx, groupID)
}

// GetGroupsForTicket returns groups whose scope covers the given CTI.
func (s *Service) GetGroupsForTicket(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]Group, error) {
	return s.store.ListGroupsInScope(ctx, categoryID, typeID)
}
