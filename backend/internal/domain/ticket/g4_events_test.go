package ticket_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Writes that announced nothing (#630): each now raises its event in the
// change's own transaction, and none of them mails anybody.

func only(t *testing.T, h *harness) notification.Event {
	t.Helper()
	require.Len(t, h.dispatcher.events, 1, "one change, one event")
	return h.dispatcher.events[0]
}

// A staff mail or a requester mail is queued only for an event that carries an
// address or may mean staff mail. None of these do.
func requireNoMail(t *testing.T, ev notification.Event) {
	t.Helper()
	require.Empty(t, ev.Recipient)
	require.False(t, ev.GuestLink)
	require.False(t, ev.MailsStaff())
	require.False(t, notification.Carries("email", ev))
}

// ── RemoveLink ───────────────────────────────────────────────────────────────

func TestRemoveLink_AnnouncesUnlinkedWithTargetAndType(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source, target := h.seedOpen(), h.seedOpen()
	agent := uuid.New()
	actor := ticket.Actor{UserID: &agent, Role: user.RoleStaff}
	require.NoError(t, h.svc.AddLink(ctx, source.ID, target.ID, ticket.LinkDuplicateOf, actor))
	h.dispatcher.events = nil

	require.NoError(t, h.svc.RemoveLink(ctx, source.ID, target.ID, ticket.LinkDuplicateOf, actor))

	links, _ := h.store.ListLinks(ctx, source.ID)
	require.Empty(t, links)
	ev := only(t, h)
	require.Equal(t, notification.EventTicketUnlinked, ev.Type)
	require.Equal(t, []notification.EventType{notification.EventTicketUnlinked}, ev.Kinds)
	require.Equal(t, source.ID, ev.TicketID, "on the source ticket, as ticket.linked is")
	require.Equal(t, &agent, ev.ActorID)
	require.Equal(t, target.ID, ev.Payload["target_id"])
	require.Equal(t, ticket.LinkDuplicateOf, ev.Payload["link_type"])
	require.NotNil(t, ev.Ticket, "the webhook filter needs the ticket")
	requireNoMail(t, ev)
	// D41: AddLink writes no audit entry, so neither does removing one.
	require.Empty(t, h.auditStore.entries)
}

// Nothing to remove, nothing to announce: the call is still a success, as the
// delete was before.
func TestRemoveLink_NoSuchLinkAnnouncesNothing(t *testing.T) {
	h := newHarness(t)
	source, target := h.seedOpen(), h.seedOpen()
	agent := uuid.New()

	require.NoError(t, h.svc.RemoveLink(context.Background(), source.ID, target.ID, ticket.LinkRelatedTo,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff}))
	require.Empty(t, h.dispatcher.events)
}

// A service credential with no person behind it can remove a link (it could
// before); the event then names no actor.
func TestRemoveLink_NoActorPersonIsAllowed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source, target := h.seedOpen(), h.seedOpen()
	agent := uuid.New()
	require.NoError(t, h.svc.AddLink(ctx, source.ID, target.ID, ticket.LinkRelatedTo, ticket.Actor{UserID: &agent, Role: user.RoleStaff}))
	h.dispatcher.events = nil

	require.NoError(t, h.svc.RemoveLink(ctx, source.ID, target.ID, ticket.LinkRelatedTo, ticket.Actor{Role: user.RoleAdmin}))
	require.Nil(t, only(t, h).ActorID)
}

// Both ticket rows are locked, smaller id first, whichever end is the source:
// AddLink's order, which ResolveAsDuplicate shares (#210). A different order
// is the deadlock.
func TestRemoveLink_LocksBothTicketsInAddLinksOrder(t *testing.T) {
	for _, swap := range []bool{false, true} {
		h := newHarness(t)
		ctx := context.Background()
		a, b := h.seedOpen(), h.seedOpen()
		if bytes.Compare(b.ID[:], a.ID[:]) < 0 {
			a, b = b, a
		}
		source, target := a, b
		if swap {
			source, target = b, a
		}
		agent := uuid.New()
		actor := ticket.Actor{UserID: &agent, Role: user.RoleStaff}
		require.NoError(t, h.svc.AddLink(ctx, source.ID, target.ID, ticket.LinkRelatedTo, actor))
		addOrder := append([]uuid.UUID(nil), h.store.lockOrder...)
		h.store.lockOrder = nil

		require.NoError(t, h.svc.RemoveLink(ctx, source.ID, target.ID, ticket.LinkRelatedTo, actor))
		require.Equal(t, []uuid.UUID{a.ID, b.ID}, h.store.lockOrder, "smaller id first")
		require.Equal(t, addOrder, h.store.lockOrder, "the same order AddLink takes")
	}
}

func TestRemoveLink_FailureAnnouncesNothing(t *testing.T) {
	h := newHarness(t)
	agent := uuid.New()
	err := h.svc.RemoveLink(context.Background(), uuid.New(), uuid.New(), ticket.LinkRelatedTo,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.Error(t, err, "a ticket that is not there is not unlinked")
	require.Empty(t, h.dispatcher.events)
}

// ── attachments ──────────────────────────────────────────────────────────────

func TestCreateAttachment_AnnouncesAttachmentAdded(t *testing.T) {
	owner, staffID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name  string
		actor ticket.Actor
		actID *uuid.UUID
	}{
		{"staff", ticket.Actor{UserID: &staffID, Role: user.RoleStaff}, &staffID},
		{"the requester", ticket.Actor{UserID: &owner, Role: user.RoleUser}, &owner},
		{"a guest", ticket.Actor{Role: user.RoleUser}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.seedOpenTicket(owner)
			att := ticket.Attachment{ID: uuid.New(), TicketID: tk.ID, Filename: "SECRET-NAME.pdf"}

			require.NoError(t, h.svc.CreateAttachment(context.Background(), att, tc.actor))

			ev := only(t, h)
			require.Equal(t, notification.EventTicketAttachmentAdded, ev.Type)
			require.Equal(t, tk.ID, ev.TicketID)
			require.Equal(t, tc.actID, ev.ActorID)
			require.Equal(t, map[string]any{"attachment_id": att.ID}, ev.Payload, "an id, never the file name")
			require.NotNil(t, ev.Ticket)
			requireNoMail(t, ev) // D39
			require.Empty(t, h.auditStore.entries)
		})
	}
}

func TestCreateGuestAttachment_AnnouncesAttachmentAdded(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpenTicket(uuid.New())
	att := ticket.Attachment{ID: uuid.New(), TicketID: tk.ID, UploadedByGuest: true}

	require.NoError(t, h.svc.CreateGuestAttachment(context.Background(), att, ticket.Actor{Role: user.RoleUser}, 5))

	ev := only(t, h)
	require.Equal(t, notification.EventTicketAttachmentAdded, ev.Type)
	require.Nil(t, ev.ActorID)
	require.Equal(t, att.ID, ev.Payload["attachment_id"])
	requireNoMail(t, ev)
}

// The announcement is part of the transaction that writes the row: a refused
// upload, and one that fails to write, announce nothing.
func TestCreateAttachment_RefusedOrFailedAnnouncesNothing(t *testing.T) {
	t.Run("closed ticket, requester", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedClosed()
		owner := *tk.ReporterUserID
		err := h.svc.CreateAttachment(context.Background(), ticket.Attachment{ID: uuid.New(), TicketID: tk.ID},
			ticket.Actor{UserID: &owner, Role: user.RoleUser})
		require.ErrorIs(t, err, ticket.ErrClosed)
		require.Empty(t, h.dispatcher.events)
	})
	t.Run("the row cannot be written", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpenTicket(uuid.New())
		staffID := uuid.New()
		h.store.errCreateAttachment = errors.New("boom")
		err := h.svc.CreateAttachment(context.Background(), ticket.Attachment{ID: uuid.New(), TicketID: tk.ID},
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.Error(t, err)
		require.Empty(t, h.dispatcher.events)
	})
	t.Run("guest over the cap", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpenTicket(uuid.New())
		err := h.svc.CreateGuestAttachment(context.Background(), ticket.Attachment{ID: uuid.New(), TicketID: tk.ID},
			ticket.Actor{Role: user.RoleUser}, 0)
		require.ErrorIs(t, err, ticket.ErrGuestAttachmentLimit)
		require.Empty(t, h.dispatcher.events)
	})
}

// ── follow and unfollow ──────────────────────────────────────────────────────

func TestFollow_AnnouncesFollowedWithThePersonsID(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tk := h.seedOpen()
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}

	require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
	ev := only(t, h)
	require.Equal(t, notification.EventTicketFollowed, ev.Type)
	require.Equal(t, tk.ID, ev.TicketID)
	require.Equal(t, &me, ev.ActorID)
	require.Equal(t, map[string]any{"user_id": me}, ev.Payload, "the person's id, not their name")
	requireNoMail(t, ev)

	h.dispatcher.events = nil
	require.NoError(t, h.svc.Unfollow(ctx, tk.ID, actor))
	ev = only(t, h)
	require.Equal(t, notification.EventTicketUnfollowed, ev.Type)
	require.Equal(t, map[string]any{"user_id": me}, ev.Payload)
	requireNoMail(t, ev)
}

// Following twice, or unfollowing when not following, changes nothing and so
// announces nothing: the same rule as the audit entry.
func TestFollow_OnlyAChangeIsAnnounced(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	tk := h.seedOpen()
	me := uuid.New()
	actor := ticket.Actor{UserID: &me, Role: user.RoleStaff}

	require.NoError(t, h.svc.Unfollow(ctx, tk.ID, actor))
	require.Empty(t, h.dispatcher.events, "was not following")
	require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
	require.NoError(t, h.svc.Follow(ctx, tk.ID, actor))
	require.Len(t, h.dispatcher.events, 1, "the repeat is not a second follow")
}

func TestFollow_RefusedAnnouncesNothing(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	me := uuid.New()
	h.store.notPeople = map[uuid.UUID]bool{me: true}

	require.ErrorIs(t, h.svc.Follow(context.Background(), tk.ID, ticket.Actor{UserID: &me, Role: user.RoleStaff}), ticket.ErrForbidden)
	require.ErrorIs(t, h.svc.Follow(context.Background(), tk.ID, ticket.Actor{Role: user.RoleStaff}), ticket.ErrForbidden)
	require.Empty(t, h.dispatcher.events)
}

// An audit entry that fails rolls the follow back, and the announcement with it.
func TestFollow_AuditFailureAnnouncesNothing(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	me := uuid.New()
	h.auditStore.err = errors.New("audit down")

	require.Error(t, h.svc.Follow(context.Background(), tk.ID, ticket.Actor{UserID: &me, Role: user.RoleStaff}))
	require.Empty(t, h.dispatcher.events)
}

// ── bulk unassignment ────────────────────────────────────────────────────────

func TestUnassignForUser_AnnouncesEachTicketWithStaffMailSuppressed(t *testing.T) {
	h := newHarness(t)
	leaving, admin, group := uuid.New(), uuid.New(), uuid.New()
	a, b := h.seedOpen(), h.seedOpen()
	a.AssigneeUserID, b.AssigneeUserID = &leaving, &leaving
	a.AssigneeGroupID = &group // the group stays: its members must not be mailed either
	h.store.seed(a)
	h.store.seed(b)
	done := h.seedResolved(uuid.New())
	done.AssigneeUserID = &leaving
	h.store.seed(done) // history, left alone
	h.dispatcher.events = nil

	moved, err := h.svc.UnassignForUser(context.Background(), admin, leaving)
	require.NoError(t, err)
	require.Equal(t, 2, moved)

	require.Len(t, h.dispatcher.events, 2, "one event per ticket (#506)")
	seen := map[uuid.UUID]notification.Event{}
	for _, ev := range h.dispatcher.events {
		seen[ev.TicketID] = ev
		require.Equal(t, notification.EventTicketAssigned, ev.Type)
		require.Equal(t, &admin, ev.ActorID, "the deleting admin")
		require.True(t, ev.SuppressStaffMail)
		require.False(t, ev.MailsStaff())
		require.False(t, notification.Carries("email", ev))
		require.True(t, notification.Carries("webhook", ev))
		require.Equal(t, []notification.Change{notification.IDChange("assignee_user_id", &leaving, nil)}, ev.Changes)
		require.Nil(t, ev.Ticket.AssigneeUserID, "the ticket as it committed")
	}
	require.Contains(t, seen, a.ID)
	require.Contains(t, seen, b.ID)
	require.Equal(t, &group, seen[a.ID].Ticket.AssigneeGroupID)
}

// The follow dropped with the assignment is announced too, in the same event,
// as an opt-in kind.
func TestUnassignForUser_ADroppedFollowRidesTheSameEvent(t *testing.T) {
	h := newHarness(t)
	leaving, admin := uuid.New(), uuid.New()
	tk := h.seedOpen()
	tk.AssigneeUserID = &leaving
	h.store.seed(tk)
	require.NoError(t, h.svc.Follow(context.Background(), tk.ID, ticket.Actor{UserID: &leaving, Role: user.RoleStaff}))
	h.dispatcher.events = nil

	_, err := h.svc.UnassignForUser(context.Background(), admin, leaving)
	require.NoError(t, err)

	ev := only(t, h)
	require.Equal(t, []notification.EventType{notification.EventTicketAssigned, notification.EventTicketUnfollowed}, ev.Kinds)
	require.Equal(t, leaving, ev.Payload["user_id"])
	require.True(t, ev.SuppressStaffMail)
}

func TestDeleteGroup_AnnouncesEveryTicketItHeldWithStaffMailSuppressed(t *testing.T) {
	h := newHarness(t)
	group, other, admin := uuid.New(), uuid.New(), uuid.New()
	open, closed := h.seedOpen(), h.seedClosed()
	open.AssigneeGroupID, closed.AssigneeGroupID = &group, &group
	h.store.seed(open)
	h.store.seed(closed)
	elsewhere := h.seedOpen()
	elsewhere.AssigneeGroupID = &other
	h.store.seed(elsewhere)
	h.dispatcher.events = nil

	moved, err := h.svc.DeleteGroup(context.Background(), &admin, group)
	require.NoError(t, err)
	require.Equal(t, 2, moved)

	require.Len(t, h.dispatcher.events, 2, "a group delete announces to webhooks, once per ticket")
	for _, ev := range h.dispatcher.events {
		require.Contains(t, []uuid.UUID{open.ID, closed.ID}, ev.TicketID)
		require.Equal(t, notification.EventTicketAssigned, ev.Type)
		require.Equal(t, &admin, ev.ActorID)
		require.Equal(t, []notification.Change{notification.IDChange("assignee_group_id", &group, nil)}, ev.Changes)
		require.Nil(t, ev.Ticket.AssigneeGroupID)
		// Mails nobody: no email row is queued, so the fan-out never runs.
		require.True(t, ev.SuppressStaffMail)
		require.False(t, notification.Carries("email", ev))
	}
}

func TestDeleteGroup_NoActorPersonIsAnnouncedWithoutOne(t *testing.T) {
	h := newHarness(t)
	group := uuid.New()
	tk := h.seedOpen()
	tk.AssigneeGroupID = &group
	h.store.seed(tk)
	h.dispatcher.events = nil

	_, err := h.svc.DeleteGroup(context.Background(), nil, group)
	require.NoError(t, err)
	require.Nil(t, only(t, h).ActorID)
}

// If the delete fails, the unassignments roll back and so do their events.
func TestDeleteGroup_FailureAnnouncesNothing(t *testing.T) {
	h := newHarness(t)
	group := uuid.New()
	tk := h.seedOpen()
	tk.AssigneeGroupID = &group
	h.store.seed(tk)
	h.store.errDeleteGroup = errors.New("boom")
	h.dispatcher.events = nil

	_, err := h.svc.DeleteGroup(context.Background(), nil, group)
	require.Error(t, err)
	require.Empty(t, h.dispatcher.events)
}
