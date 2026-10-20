package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Staff mail waits in the queue and can be retried for an hour, and the ticket
// moves meanwhile. At send time the person is checked again against the ticket
// as it is then (#441).

func seedStaffMember(t *testing.T, h *harness, name string) user.User {
	t.Helper()
	u, err := h.userSvc.Create(context.Background(), user.CreateUserInput{
		Email: name + "@test.local", DisplayName: name, Role: user.RoleStaff, Password: "password",
	})
	require.NoError(t, err)
	return u
}

func adminActor(h *harness) ticket.Actor {
	return ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}
}

func TestStillEntitled_ByReason(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	other := seedStaffMember(t, h, "other")
	staff, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	tk := newStaffTicket(t, h)
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, &g.ID, adminActor(h))
	require.NoError(t, err)

	entitled := func(u user.User, id uuid.UUID, r notification.StaffSwitch) bool {
		ok, err := h.srv.StillEntitled(ctx, u, id, r)
		require.NoError(t, err)
		return ok
	}

	// The person who holds it, and a member of the group that does.
	require.True(t, entitled(staff, tk.ID, notification.SwitchAssigned))
	require.True(t, entitled(staff, tk.ID, notification.SwitchActivity))
	require.True(t, entitled(staff, tk.ID, notification.SwitchGroup))
	// Somebody who is neither.
	require.False(t, entitled(other, tk.ID, notification.SwitchAssigned))
	require.False(t, entitled(other, tk.ID, notification.SwitchActivity))
	require.False(t, entitled(other, tk.ID, notification.SwitchGroup))
	// A ticket that is gone has nothing to tell anyone.
	require.False(t, entitled(staff, uuid.New(), notification.SwitchAssigned))
}

// A follower is entitled while they can see the ticket (#391), which depends on
// the groups they belong to once scope enforcement is on. PR-D of #464 queues
// the mail; the rule is settled here.
func TestStillEntitled_AFollowerCanStillSeeTheTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	staff, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	tk := newStaffTicket(t, h) // in the harness category, held by nobody
	// A follower is entitled only while the follow row exists (#441).
	_, err = h.ticketStore.AddFollower(ctx, tk.ID, h.staffID)
	require.NoError(t, err)

	follows := func() bool {
		ok, err := h.srv.StillEntitled(ctx, staff, tk.ID, notification.SwitchFollow)
		require.NoError(t, err)
		return ok
	}

	require.True(t, follows(), "scope enforcement is off: staff see everything")

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyTicketScopeEnforced, true))
	require.False(t, follows(), "enforcement is on and no group of theirs covers the ticket's category")

	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: h.catID}))
	require.True(t, follows(), "their group now covers it")
}

func TestStaffEmail_MailIsDroppedWhenTheTicketWasReassignedBeforeItWasSent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	tk := newStaffTicket(t, h)
	other := seedStaffMember(t, h, "other")

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_user_id": h.staffID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	// Passed on before the worker ran.
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &other.ID, nil, adminActor(h))
	require.NoError(t, err)

	got := m.recipients(t)
	require.NotContains(t, got, "staff@test.local", "the first assignee no longer holds it")
}

func TestStaffEmail_MailIsDroppedWhenTheMemberLeftTheGroupBeforeItWasSent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	m := wireStaffMail(t, h)
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	tk := newStaffTicket(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, h.groupSvc.RemoveMember(ctx, g.ID, h.staffID))

	require.Empty(t, m.recipients(t))
}

// Erik's rule for replies: when somebody holds the ticket, only they are
// mailed. The group is mailed only while nobody does.
func TestStaffEmail_AReplyOnAHeldTicketMailsOnlyTheAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	colleague := seedStaffMember(t, h, "colleague")
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, colleague.ID))
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	tk := newStaffTicket(t, h)
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, &g.ID, adminActor(h))
	require.NoError(t, err)
	m := wireStaffMail(t, h)

	res := h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "hello"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))

	// With nobody holding it, the group hears about it.
	_, err = h.ticketSvc.Assign(ctx, tk.ID, nil, &g.ID, adminActor(h))
	require.NoError(t, err)
	m.recipients(t) // the assignment mail, not what is under test
	res = h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "again"})
	res.Body.Close()
	require.ElementsMatch(t, []string{"staff@test.local", "colleague@test.local"}, m.recipients(t))
}

// A staff member who files a ticket is its requester: their reply is the
// requester replying, and mails whoever holds it.
func TestStaffEmail_AStaffMembersReplyOnTheirOwnTicketIsARequesterReply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		Subject: "Mine", Description: "d", CategoryID: h.catID, ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.adminID, nil, adminActor(h))
	require.NoError(t, err)
	m := wireStaffMail(t, h)

	res := h.do(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "any news?"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.Equal(t, []string{"admin@test.local"}, m.recipients(t))
}

// A service user (the identity an OAuth client acts as, #490) can end up in a
// group. It has no inbox: it must get no group mail, however the ticket reaches
// the group.
func TestStaffEmail_AServiceUserInAGroupIsNeverMailed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	bot := uuid.New()
	_, err := h.tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, 'oauth-bot@service-account.invalid', 'Bot', 'staff', TRUE)`, bot)
	require.NoError(t, err)
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.staffID))
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, bot))
	m := wireStaffMail(t, h)

	// Assigned to the group.
	tk := newStaffTicket(t, h)
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{"assignee_group_id": g.ID})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))

	// A requester replies on it.
	res = h.doAsUser(t, http.MethodPost, fmt.Sprintf("/api/v1/tickets/%s/replies", tk.ID), map[string]any{"body": "hello"})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, []string{"staff@test.local"}, m.recipients(t))
}
