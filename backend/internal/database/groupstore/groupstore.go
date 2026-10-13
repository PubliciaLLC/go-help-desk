// Package groupstore implements domain/group.Store against PostgreSQL.
package groupstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// Store implements group.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Create(ctx context.Context, g group.Group) error {
	err := s.q.CreateGroup(ctx, dbgen.CreateGroupParams{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
	})
	if err != nil {
		if isGroupNameTaken(err) {
			return fmt.Errorf("%w: %s", group.ErrNameTaken, g.Name)
		}
		return fmt.Errorf("creating group: %w", err)
	}
	return nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (group.Group, error) {
	r, err := s.q.GetGroup(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return group.Group{}, fmt.Errorf("group %s: %w", id, group.ErrNotFound)
		}
		return group.Group{}, fmt.Errorf("getting group %s: %w", id, err)
	}
	return group.Group{ID: r.ID, Name: r.Name, Description: r.Description}, nil
}

func (s *Store) Update(ctx context.Context, g group.Group) error {
	err := s.q.UpdateGroup(ctx, dbgen.UpdateGroupParams{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
	})
	if err != nil {
		if isGroupNameTaken(err) {
			return fmt.Errorf("%w: %s", group.ErrNameTaken, g.Name)
		}
		return fmt.Errorf("updating group: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteGroup(ctx, id)
}

func (s *Store) List(ctx context.Context) ([]group.Group, error) {
	rows, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing groups: %w", err)
	}
	out := make([]group.Group, len(rows))
	for i, r := range rows {
		out[i] = group.Group{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

func (s *Store) AddMember(ctx context.Context, groupID, userID uuid.UUID) error {
	err := s.q.AddGroupMember(ctx, dbgen.AddGroupMemberParams{GroupID: groupID, UserID: userID})
	if err != nil {
		if isGroupMemberGroupFKViolation(err) {
			return fmt.Errorf("group %s: %w", groupID, group.ErrNotFound)
		}
		if isGroupMemberUserFKViolation(err) {
			return fmt.Errorf("user %s: %w", userID, group.ErrInvalidReference)
		}
		return fmt.Errorf("adding group member: %w", err)
	}
	return nil
}

func (s *Store) RemoveMember(ctx context.Context, groupID, userID uuid.UUID) error {
	return s.q.RemoveGroupMember(ctx, dbgen.RemoveGroupMemberParams{GroupID: groupID, UserID: userID})
}

func (s *Store) ListMembers(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	return s.q.ListGroupMembers(ctx, groupID)
}

func (s *Store) ListGroupsForUser(ctx context.Context, userID uuid.UUID) ([]group.Group, error) {
	rows, err := s.q.ListGroupsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing groups for user: %w", err)
	}
	out := make([]group.Group, len(rows))
	for i, r := range rows {
		out[i] = group.Group{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

func (s *Store) AddScope(ctx context.Context, sc group.GroupScope) error {
	err := s.q.AddGroupScope(ctx, dbgen.AddGroupScopeParams{
		GroupID:    sc.GroupID,
		CategoryID: sc.CategoryID,
		TypeID:     database.NullUUID(sc.TypeID),
	})
	if err != nil {
		if isGroupScopeGroupFKViolation(err) {
			return fmt.Errorf("group %s: %w", sc.GroupID, group.ErrNotFound)
		}
		if isGroupScopeCategoryFKViolation(err) || isGroupScopeTypeFKViolation(err) || isGroupScopeTypeCategoryFKViolation(err) {
			return fmt.Errorf("scope reference: %w", group.ErrInvalidReference)
		}
		return fmt.Errorf("adding scope: %w", err)
	}
	return nil
}

func (s *Store) RemoveScope(ctx context.Context, groupID, categoryID uuid.UUID, typeID *uuid.UUID) error {
	return s.q.RemoveGroupScope(ctx, dbgen.RemoveGroupScopeParams{
		GroupID:    groupID,
		CategoryID: categoryID,
		TypeID:     database.NullUUID(typeID),
	})
}

func (s *Store) ListScopes(ctx context.Context, groupID uuid.UUID) ([]group.GroupScope, error) {
	rows, err := s.q.ListGroupScopes(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("listing group scopes: %w", err)
	}
	out := make([]group.GroupScope, len(rows))
	for i, r := range rows {
		out[i] = group.GroupScope{
			GroupID:    r.GroupID,
			CategoryID: r.CategoryID,
			TypeID:     database.UUIDPtr(r.TypeID),
		}
	}
	return out, nil
}

func (s *Store) ListGroupsInScope(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]group.Group, error) {
	rows, err := s.q.ListGroupsInScope(ctx, dbgen.ListGroupsInScopeParams{
		CategoryID: categoryID,
		TypeID:     database.NullUUID(typeID),
	})
	if err != nil {
		return nil, fmt.Errorf("listing groups in scope: %w", err)
	}
	out := make([]group.Group, len(rows))
	for i, r := range rows {
		out[i] = group.Group{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

func (s *Store) ListGroupsForExactScope(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]group.Group, error) {
	rows, err := s.q.ListGroupsForExactScope(ctx, dbgen.ListGroupsForExactScopeParams{
		CategoryID: categoryID,
		TypeID:     database.NullUUID(typeID),
	})
	if err != nil {
		return nil, fmt.Errorf("listing groups for exact scope: %w", err)
	}
	out := make([]group.Group, len(rows))
	for i, r := range rows {
		out[i] = group.Group{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

func isGroupNameTaken(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "groups_name_key"
}

func isGroupMemberGroupFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_members_group_id_fkey"
}

func isGroupMemberUserFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_members_user_id_fkey"
}

func isGroupScopeGroupFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_scopes_group_id_fkey"
}

func isGroupScopeCategoryFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_scopes_category_id_fkey"
}

func isGroupScopeTypeFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_scopes_type_id_fkey"
}

// group_scopes_type_category_fk (migration 13) fires when the type exists but
// belongs to a different category than the one in the request body.
func isGroupScopeTypeCategoryFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "group_scopes_type_category_fk"
}
