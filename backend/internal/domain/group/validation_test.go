package group_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// A blank name is ErrValidation on create and update (#418), and update trims.
func TestGroupService_BlankNameIsErrValidation(t *testing.T) {
	svc := group.NewService(newFakeGroupStore())
	ctx := context.Background()

	_, err := svc.Create(ctx, "  ", "")
	require.ErrorIs(t, err, group.ErrValidation)

	g, err := svc.Create(ctx, "Ops", "")
	require.NoError(t, err)

	err = svc.Update(ctx, group.Group{ID: g.ID, Name: " "})
	require.ErrorIs(t, err, group.ErrValidation)

	require.NoError(t, svc.Update(ctx, group.Group{ID: g.ID, Name: "  Ops2  "}))
	got, err := svc.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, "Ops2", got.Name)
}
