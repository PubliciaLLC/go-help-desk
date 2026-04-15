package group_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/stretchr/testify/require"
)

// fakeGroupStore is an in-memory implementation of group.Store.
type fakeGroupStore struct {
	groups  map[uuid.UUID]group.Group
	members map[uuid.UUID][]uuid.UUID // groupID → []userID
	scopes  map[uuid.UUID][]group.GroupScope
}

func newFakeGroupStore() *fakeGroupStore {
	return &fakeGroupStore{
		groups:  make(map[uuid.UUID]group.Group),
		members: make(map[uuid.UUID][]uuid.UUID),
		scopes:  make(map[uuid.UUID][]group.GroupScope),
	}
}

func (f *fakeGroupStore) Create(_ context.Context, g group.Group) error {
	f.groups[g.ID] = g
	return nil
}
func (f *fakeGroupStore) GetByID(_ context.Context, id uuid.UUID) (group.Group, error) {
	g, ok := f.groups[id]
	if !ok {
		return group.Group{}, errors.New("not found")
	}
	return g, nil
}
func (f *fakeGroupStore) Update(_ context.Context, g group.Group) error {
	f.groups[g.ID] = g
	return nil
}
func (f *fakeGroupStore) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.groups, id)
	return nil
}
func (f *fakeGroupStore) List(_ context.Context) ([]group.Group, error) {
	out := make([]group.Group, 0, len(f.groups))
	for _, g := range f.groups {
		out = append(out, g)
	}
	return out, nil
}
func (f *fakeGroupStore) AddMember(_ context.Context, groupID, userID uuid.UUID) error {
	f.members[groupID] = append(f.members[groupID], userID)
	return nil
}
func (f *fakeGroupStore) RemoveMember(_ context.Context, groupID, userID uuid.UUID) error {
	members := f.members[groupID]
	for i, id := range members {
		if id == userID {
			f.members[groupID] = append(members[:i], members[i+1:]...)
			return nil
		}
	}
	return nil
}
func (f *fakeGroupStore) ListMembers(_ context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	return f.members[groupID], nil
}
func (f *fakeGroupStore) ListGroupsForUser(_ context.Context, userID uuid.UUID) ([]group.Group, error) {
	var out []group.Group
	for gid, members := range f.members {
		for _, uid := range members {
			if uid == userID {
				if g, ok := f.groups[gid]; ok {
					out = append(out, g)
				}
			}
		}
	}
	return out, nil
}
func (f *fakeGroupStore) AddScope(_ context.Context, sc group.GroupScope) error {
	f.scopes[sc.GroupID] = append(f.scopes[sc.GroupID], sc)
	return nil
}
func (f *fakeGroupStore) RemoveScope(_ context.Context, groupID, categoryID uuid.UUID, typeID *uuid.UUID) error {
	scopes := f.scopes[groupID]
	for i, sc := range scopes {
		if sc.CategoryID == categoryID && uuidPtrEq(sc.TypeID, typeID) {
			f.scopes[groupID] = append(scopes[:i], scopes[i+1:]...)
			return nil
		}
	}
	return nil
}
func (f *fakeGroupStore) ListScopes(_ context.Context, groupID uuid.UUID) ([]group.GroupScope, error) {
	return f.scopes[groupID], nil
}
func (f *fakeGroupStore) ListGroupsInScope(_ context.Context, categoryID uuid.UUID, typeID *uuid.UUID) ([]group.Group, error) {
	var out []group.Group
	for gid, scopes := range f.scopes {
		for _, sc := range scopes {
			if sc.IsInScope(categoryID, typeID) {
				if g, ok := f.groups[gid]; ok {
					out = append(out, g)
				}
				break
			}
		}
	}
	return out, nil
}

func uuidPtrEq(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGroupService_Create_Valid(t *testing.T) {
	svc := group.NewService(newFakeGroupStore())
	g, err := svc.Create(context.Background(), "Network", "Network team")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, g.ID)
	require.Equal(t, "Network", g.Name)
}

func TestGroupService_Create_EmptyName(t *testing.T) {
	svc := group.NewService(newFakeGroupStore())
	_, err := svc.Create(context.Background(), "   ", "desc")
	require.Error(t, err)
}

func TestGroupService_AddScope_ListScopes_RemoveScope(t *testing.T) {
	svc := group.NewService(newFakeGroupStore())
	g, err := svc.Create(context.Background(), "Infra", "")
	require.NoError(t, err)

	catID := uuid.New()
	typeID := uuid.New()
	sc := group.GroupScope{GroupID: g.ID, CategoryID: catID, TypeID: &typeID}

	require.NoError(t, svc.AddScope(context.Background(), sc))

	scopes, err := svc.ListScopes(context.Background(), g.ID)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Equal(t, catID, scopes[0].CategoryID)

	require.NoError(t, svc.RemoveScope(context.Background(), g.ID, catID, &typeID))

	scopes, err = svc.ListScopes(context.Background(), g.ID)
	require.NoError(t, err)
	require.Empty(t, scopes)
}
