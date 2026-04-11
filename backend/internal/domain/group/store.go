package group

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for groups and their memberships/scopes.
type Store interface {
	// Groups
	Create(ctx context.Context, g Group) error
	GetByID(ctx context.Context, id uuid.UUID) (Group, error)
	Update(ctx context.Context, g Group) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]Group, error)

	// Members
	AddMember(ctx context.Context, groupID, userID uuid.UUID) error
	RemoveMember(ctx context.Context, groupID, userID uuid.UUID) error
	ListMembers(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
	ListGroupsForUser(ctx context.Context, userID uuid.UUID) ([]Group, error)

	// Scopes
	AddScope(ctx context.Context, s GroupScope) error
	RemoveScope(ctx context.Context, groupID, categoryID uuid.UUID, typeID *uuid.UUID) error
	ListScopes(ctx context.Context, groupID uuid.UUID) ([]GroupScope, error)

	// ListGroupsInScope returns all groups whose scope covers the given CTI.
	ListGroupsInScope(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]Group, error)
}
