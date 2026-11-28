package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Unfollowing stops mail that is already queued: the follow row is checked
// again when the mail is sent.
func TestFollowers_UnfollowingBeforeTheSendStopsTheMail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	staff := h.session(t, asStaff)
	followAs(t, h, staff, tk)
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk) // queued, not yet sent
	require.Equal(t, http.StatusNoContent, status(t, h.doSession(t, staff, http.MethodDelete, followPath(tk.ID), nil)))

	require.Empty(t, m.recipients(t))
}

// Erik, 2026-10-14: the assignee and the assigned group follow the ticket from
// a practical point of view. Following on top does not bring back mail they
// switched off.
func TestFollowers_TheAssigneeWithTheirSwitchOffGetsNothingByFollowing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(context.Background(), tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)
	followAs(t, h, h.session(t, asStaff), tk)
	m := wireStaffMail(t, h)
	setPrefs(t, h, user.EmailPreferencesPatch{Activity: flag(false), Assigned: flag(false)})

	requesterReplies(t, h, tk)
	require.Empty(t, m.recipients(t))

}

// Reassigned away, the old assignee's follow goes with the assignment: they
// get no follower mail afterwards, and the removal is audited for admins.
func TestFollowers_ReassigningDropsTheOldAssigneesFollow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := newStaffTicket(t, h)
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)
	followAs(t, h, h.session(t, asStaff), tk)
	other := seedStaffMember(t, h, "other")
	m := wireStaffMail(t, h)

	// Over HTTP, by PATCH.
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": other.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"other@test.local"}, m.recipients(t))

	ids, err := h.ticketSvc.ListMailableFollowerIDs(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, ids)

	requesterReplies(t, h, tk)
	require.Equal(t, []string{"other@test.local"}, m.recipients(t), "the old assignee is no longer mailed")

	acts := ticketAuditActions(t, h, h.session(t, asAdmin), tk.ID)
	require.Equal(t, []string{"followed", "unfollowed"}, onlyFollow(acts))
}

func TestFollowers_AGroupMemberWithTheGroupSwitchOffGetsNothingByFollowing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	tk := newStaffTicket(t, h)
	followAs(t, h, h.session(t, asStaff), tk)
	setPrefs(t, h, user.EmailPreferencesPatch{Group: flag(false)})
	m := wireStaffMail(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, m.recipients(t), "assignment")

	requesterReplies(t, h, tk)
	require.Empty(t, m.recipients(t), "reply")
}

// Mail queued to a follower is not dropped because the ticket was then
// assigned to that same person: they still hear of the reply that was queued
// before.
func TestFollowers_BeingAssignedBeforeTheSendDoesNotLoseTheQueuedReplyMail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := newStaffTicket(t, h) // nobody holds it
	followAs(t, h, h.session(t, asStaff), tk)
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk) // follower mail queued
	_, err := h.ticketSvc.Assign(context.Background(), tk.ID, &h.staffID, nil, adminActor(h))
	require.NoError(t, err)

	// The assignment mail and the reply mail.
	require.Equal(t, []string{"staff@test.local", "staff@test.local"}, m.recipients(t))
}

// Mia is in the group that holds the ticket with Ann, and follows it. The
// group is not mailed for a reply while Ann holds it, so Mia's follow counts.
func TestFollowers_AGroupMemberWhoFollowsIsMailedWhileAnotherMemberHoldsTheTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	ann := seedStaffMember(t, h, "ann")
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, ann.ID))
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID)) // Mia
	tk := newStaffTicket(t, h)
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &ann.ID, &g.ID, adminActor(h))
	require.NoError(t, err)
	followAs(t, h, h.session(t, asStaff), tk)
	setPrefs(t, h, user.EmailPreferencesPatch{Group: flag(false)})
	m := wireStaffMail(t, h)

	requesterReplies(t, h, tk)

	require.ElementsMatch(t, []string{"ann@test.local", "staff@test.local"}, m.recipients(t))
}
