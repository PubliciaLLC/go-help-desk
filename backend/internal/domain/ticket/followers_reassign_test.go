package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Erik, 2026-10-14: when a ticket is reassigned away from someone, their
// follow goes. Only the assignee; they click Follow again to keep the mail.

type reassignFixture struct {
	h        *harness
	tk       ticket.Ticket
	old, new uuid.UUID
	admin    ticket.Actor
}

func newReassignFixture(t *testing.T) *reassignFixture {
	t.Helper()
	h := newHarness(t)
	f := &reassignFixture{h: h, old: uuid.New(), new: uuid.New()}
	adminID := uuid.New()
	f.admin = ticket.Actor{UserID: &adminID, Role: user.RoleAdmin}
	f.tk = h.seedResolved(uuid.New())
	stored := f.tk
	stored.AssigneeUserID = &f.old
	h.store.seed(stored)
	return f
}

func (f *reassignFixture) follow(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.NoError(t, f.h.svc.Follow(context.Background(), f.tk.ID, ticket.Actor{UserID: &id, Role: user.RoleStaff}))
	f.h.auditStore.entries = nil // only what the reassignment writes
}

func (f *reassignFixture) followers(t *testing.T) []uuid.UUID {
	t.Helper()
	ids, err := f.h.svc.ListMailableFollowerIDs(context.Background(), f.tk.ID)
	require.NoError(t, err)
	return ids
}

func (f *reassignFixture) unfollowEntries() (n int) {
	for _, e := range f.h.auditStore.entries {
		if e.Action == ticket.AuditActionUnfollowed {
			n++
		}
	}
	return n
}

func TestReassign_RemovesTheOldAssigneesFollowAndAuditsIt(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)

	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.new, nil, f.admin)
	require.NoError(t, err)

	require.Empty(t, f.followers(t))
	require.Equal(t, 1, f.unfollowEntries())
	for _, e := range f.h.auditStore.entries {
		if e.Action == ticket.AuditActionUnfollowed {
			require.Equal(t, f.admin.UserID, e.ActorID, "the reassigning actor")
			require.Equal(t, f.old.String(), e.Before["assignee_user_id"], "whose follow went")
		}
	}
}

func TestReassign_UnassigningRemovesIt(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)

	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, nil, nil, f.admin)
	require.NoError(t, err)

	require.Empty(t, f.followers(t))
	require.Equal(t, 1, f.unfollowEntries())
}

func TestReassign_NoFollowRowMeansNoEntry(t *testing.T) {
	f := newReassignFixture(t)

	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.new, nil, f.admin)
	require.NoError(t, err)

	require.Zero(t, f.unfollowEntries())
}

func TestReassign_TheSamePersonChangesNothing(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)
	g := uuid.New()

	// Again to the same person, and the same person with a group added.
	for _, grp := range []*uuid.UUID{nil, &g} {
		_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.old, grp, f.admin)
		require.NoError(t, err)
	}

	require.Equal(t, []uuid.UUID{f.old}, f.followers(t))
	require.Zero(t, f.unfollowEntries())
}

// Moving the ticket to another group, with the assignee kept, leaves every
// follow alone, and a group member's follow is never touched by a move.
func TestReassign_AGroupMoveLeavesFollowsAlone(t *testing.T) {
	f := newReassignFixture(t)
	member := uuid.New()
	f.follow(t, f.old)
	f.follow(t, member)
	g1, g2 := uuid.New(), uuid.New()

	_, err := f.h.svc.Assign(context.Background(), f.tk.ID, &f.old, &g1, f.admin)
	require.NoError(t, err)
	_, err = f.h.svc.Assign(context.Background(), f.tk.ID, &f.old, &g2, f.admin)
	require.NoError(t, err)

	require.ElementsMatch(t, []uuid.UUID{f.old, member}, f.followers(t))
	require.Zero(t, f.unfollowEntries())

	// Reassigning the person away takes only their follow.
	_, err = f.h.svc.Assign(context.Background(), f.tk.ID, &f.new, &g2, f.admin)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{member}, f.followers(t))
}

// A departing user's open tickets are unassigned too, and that is a path that
// changes the assignee.
func TestReassign_DeletingTheAssigneeRemovesTheirFollow(t *testing.T) {
	f := newReassignFixture(t)
	f.follow(t, f.old)
	bystander := uuid.New()
	f.follow(t, bystander)

	// seedResolved's ticket is resolved, which UnassignForUser leaves alone;
	// reopen it for the purpose.
	stored := f.tk
	stored.AssigneeUserID, stored.ResolvedAt = &f.old, nil
	f.h.store.seed(stored)

	n, err := f.h.svc.UnassignForUser(context.Background(), *f.admin.UserID, f.old)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.Equal(t, []uuid.UUID{bystander}, f.followers(t))
	require.Equal(t, 1, f.unfollowEntries())
}
