package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Deleting a group returns its tickets to the queue and writes one audit entry
// for each, in the same transaction as the delete (#419).
//
// The database clears assignee_group_id on its own when the group row goes
// (ON DELETE SET NULL), which left tickets that read "assigned to a group" and
// then showed no group, with nothing in the trail saying why.
func TestDeleteGroup_UnassignsEveryTicketAndAuditsEach(t *testing.T) {
	h := newHarness(t)
	group := uuid.New()
	other := uuid.New()
	admin := uuid.New()

	a, b := h.seedOpen(), h.seedOpen()
	a.AssigneeGroupID, b.AssigneeGroupID = &group, &group
	h.store.seed(a)
	h.store.seed(b)
	elsewhere := h.seedOpen()
	elsewhere.AssigneeGroupID = &other
	h.store.seed(elsewhere)

	moved, err := h.svc.DeleteGroup(context.Background(), &admin, group)
	require.NoError(t, err)
	require.Equal(t, 2, moved)

	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got, err := h.store.GetByID(context.Background(), id)
		require.NoError(t, err)
		require.Nil(t, got.AssigneeGroupID)
	}
	still, err := h.store.GetByID(context.Background(), elsewhere.ID)
	require.NoError(t, err)
	require.NotNil(t, still.AssigneeGroupID, "another group's ticket must not be touched")

	require.Len(t, h.auditStore.entries, 2, "one entry per ticket")
	for _, e := range h.auditStore.entries {
		require.Equal(t, "unassigned", e.Action)
		require.Equal(t, "ticket", e.EntityType)
		require.Equal(t, &admin, e.ActorID)
		require.Equal(t, group.String(), e.Before["assignee_group_id"])
		require.Nil(t, e.After["assignee_group_id"])
	}
}

// If the delete itself fails, the unassignments and their audit entries are
// undone: a ticket must not lose its group while the group survives.
func TestDeleteGroup_FailureLeavesNoTrace(t *testing.T) {
	h := newHarness(t)
	group := uuid.New()
	tk := h.seedOpen()
	tk.AssigneeGroupID = &group
	h.store.seed(tk)
	h.store.errDeleteGroup = errors.New("boom")

	_, err := h.svc.DeleteGroup(context.Background(), nil, group)
	require.Error(t, err)

	got, err := h.store.GetByID(context.Background(), tk.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AssigneeGroupID, "the unassignment must roll back with the failed delete")
	require.Empty(t, h.auditStore.entries, "and so must its audit entries")
}

// And the reverse: if an audit entry cannot be written, the group stays.
func TestDeleteGroup_AuditFailureStopsTheDelete(t *testing.T) {
	h := newHarness(t)
	group := uuid.New()
	tk := h.seedOpen()
	tk.AssigneeGroupID = &group
	h.store.seed(tk)
	h.auditStore.err = errors.New("audit down")

	_, err := h.svc.DeleteGroup(context.Background(), nil, group)
	require.Error(t, err)

	got, err := h.store.GetByID(context.Background(), tk.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AssigneeGroupID)
}
