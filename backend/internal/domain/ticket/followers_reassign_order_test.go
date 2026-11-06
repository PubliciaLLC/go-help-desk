package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func auditActions(f *reassignFixture) []string {
	var actions []string
	for _, e := range f.h.auditStore.entries {
		actions = append(actions, e.Action)
	}
	return actions
}

// The feed reads the reassignment first and the follow it ended after it.
func TestReassign_TheAssignmentIsAuditedBeforeTheFollowRemoval(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)
	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.new, nil, f.admin)
	require.NoError(t, err)
	require.Equal(t, []string{"assigned", "unfollowed"}, auditActions(f))

	f2 := newReassignFixture(t)
	f2.follow(t, f2.old)
	_, err = f2.h.svc.Assign(context.Background(), f2.tk.ID, nil, nil, f2.admin)
	require.NoError(t, err)
	require.Equal(t, []string{"unassigned", "unfollowed"}, auditActions(f2))
}

// A reassignment that is refused changes nothing, the follow included.
func TestReassign_ARefusedReassignmentKeepsTheFollow(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)
	f.h.store.notPeople = map[uuid.UUID]bool{f.new: true} // not assignable

	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.new, nil, f.admin)
	require.Error(t, err)

	require.Equal(t, []uuid.UUID{f.old}, f.followers(t))
	require.Empty(t, f.h.auditStore.entries)
}
