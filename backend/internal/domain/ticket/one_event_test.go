package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// One change, one event (#506, rulings 2 and 8). Each row is a row of
// DESIGN.md's "One action, several events, today" table with its "after":
// how many events the action announces and the kinds each carries, in the
// fixed order. The old column was one event per field changed, and a different
// name for the same status move depending on which button made it.
func TestOneChange_OneEvent(t *testing.T) {
	ctx := context.Background()
	K := func(k ...notification.EventType) []notification.EventType { return k }
	const (
		created   = notification.EventTicketCreated
		reopened  = notification.EventTicketReopened
		resolved  = notification.EventTicketResolved
		closed    = notification.EventTicketClosed
		statusChg = notification.EventTicketStatusChanged
		assigned  = notification.EventTicketAssigned
		priority  = notification.EventTicketPriorityChanged
		classif   = notification.EventTicketClassified
		replied   = notification.EventTicketReplied
		note      = notification.EventTicketNoteAdded
		linked    = notification.EventTicketLinked
	)
	assignee := uuid.New()
	group := uuid.New()

	tests := []struct {
		name string
		run  func(t *testing.T, h *prioHarness)
		want [][]notification.EventType // the kinds of each event, in order
	}{
		{"staff create, routed to a group", func(t *testing.T, h *prioHarness) {
			_, err := h.svc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
				Subject: "s", CategoryID: uuid.New(), ReporterUserID: ptr(uuid.New()),
				AutoAssign: []ticket.Assignment{{GroupID: &group}},
			})
			require.NoError(t, err)
		}, [][]notification.EventType{K(created, assigned)}},
		{"create with nowhere to route it", func(t *testing.T, h *prioHarness) {
			_, err := h.svc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, Subject: "s", CategoryID: uuid.New(), ReporterUserID: ptr(uuid.New())})
			require.NoError(t, err)
		}, [][]notification.EventType{K(created)}},
		{"PATCH status + assignee + priority", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{
				StatusID: &h.inProgressStatus.ID, Assign: &ticket.Assignment{UserID: &assignee},
				Priority: ptrTo(ticket.PriorityHigh),
			}, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(statusChg, assigned, priority)}},
		{"PATCH category only", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{CTI: &ticket.Classification{CategoryID: uuid.New()}}, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(classif)}},
		{"PATCH priority only", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{Priority: ptrTo(ticket.PriorityLow)}, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(priority)}},
		{"PATCH out of Resolved (first reopen raises the priority)", func(t *testing.T, h *prioHarness) {
			tk := h.seedFinished("resolved", ticket.PriorityMedium)
			_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{StatusID: &h.inProgressStatus.ID}, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(reopened, statusChg, priority)}},
		{"PATCH out of Resolved + assignee", func(t *testing.T, h *prioHarness) {
			tk := h.seedFinished("resolved", ticket.PriorityMedium)
			_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{
				StatusID: &h.inProgressStatus.ID, Assign: &ticket.Assignment{UserID: &assignee},
			}, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(reopened, statusChg, assigned, priority)}},
		{"requester reply that reopens", func(t *testing.T, h *prioHarness) {
			tk := h.seedFinished("resolved", ticket.PriorityMedium)
			a := ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}
			_, err := h.svc.AddReply(ctx, tk.ID, "again", ticket.TextFormatPlain, false, true, "r@example.test", a, 30, h.newStatus.ID)
			require.NoError(t, err)
		}, [][]notification.EventType{K(reopened, statusChg, priority, replied)}},
		{"POST /reopen", func(t *testing.T, h *prioHarness) {
			tk := h.seedFinished("closed", ticket.PriorityMedium)
			h.policy = ticket.ReopenPolicyStaffAdmin
			_, err := h.svc.Reopen(ctx, tk.ID, h.newStatus.ID, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(reopened, statusChg, priority)}},
		{"resolve as duplicate", func(t *testing.T, h *prioHarness) {
			src, other := h.seedMedium(), h.seedOpen()
			_, err := h.svc.ResolveAsDuplicate(ctx, src.ID, other.ID, "dup", reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(resolved, statusChg, linked)}},
		// The last four rows of the table: the same move of status, the same
		// kinds, whichever button made it.
		{"PATCH status to Resolved", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.UpdateStatus(ctx, tk.ID, h.resolvedStatus.ID, reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(resolved, statusChg)}},
		{"Resolve button", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.Resolve(ctx, tk.ID, "fixed", reopenStaff)
			require.NoError(t, err)
		}, [][]notification.EventType{K(resolved, statusChg)}},
		{"Close button, or the scheduler", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			require.NoError(t, h.svc.Close(ctx, tk.ID, ticket.SystemActor))
		}, [][]notification.EventType{K(closed, statusChg)}},
		{"PATCH status to Closed", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.UpdateStatus(ctx, tk.ID, h.closedStatus.ID, reopenAdmin)
			require.NoError(t, err)
		}, [][]notification.EventType{K(closed, statusChg)}},
		{"an internal note", func(t *testing.T, h *prioHarness) {
			tk := h.seedMedium()
			_, err := h.svc.AddReply(ctx, tk.ID, "n", ticket.TextFormatPlain, true, false, "", reopenStaff, 30, h.newStatus.ID)
			require.NoError(t, err)
		}, [][]notification.EventType{K(note)}},
		{"a link", func(t *testing.T, h *prioHarness) {
			a, b := h.seedMedium(), h.seedOpen()
			require.NoError(t, h.svc.AddLink(ctx, a.ID, b.ID, ticket.LinkParentChild, reopenStaff))
		}, [][]notification.EventType{K(linked)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newPrioHarness(t)
			tc.run(t, h)
			var got [][]notification.EventType
			for _, e := range h.dispatcher.events {
				got = append(got, e.Kinds)
				require.Equal(t, e.Kinds[0], e.Type, "Type is the first kind")
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// A follow-up touches two tickets: one event for each (ruling 8).
func TestOneChange_AFollowUpIsOneEventPerTicket(t *testing.T) {
	h := newPrioHarness(t)
	closed := h.seedFinished("closed", ticket.PriorityMedium)
	_, err := h.svc.CreateFollowUp(context.Background(), closed.ID, "HD", ticket.ChannelWeb, reopenStaff)
	require.NoError(t, err)

	require.Len(t, h.dispatcher.events, 2)
	require.Equal(t, []notification.EventType{notification.EventTicketCreated}, h.dispatcher.events[0].Kinds)
	require.NotEqual(t, closed.ID, h.dispatcher.events[0].TicketID)
	require.Equal(t, []notification.EventType{notification.EventTicketLinked}, h.dispatcher.events[1].Kinds)
	require.Equal(t, closed.ID, h.dispatcher.events[1].TicketID)
}

// Ruling 5: routing happens inside the create's transaction. The first
// candidate the assignment accepts is the one chosen; a refused one is skipped.
func TestCreate_RoutingIsInsideTheCreateTransaction(t *testing.T) {
	h := newHarness(t)
	gone, alive := uuid.New(), uuid.New()
	h.store.notPeople = map[uuid.UUID]bool{gone: true}

	tk, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "s", CategoryID: uuid.New(), ReporterUserID: ptr(uuid.New()),
		AutoAssign: []ticket.Assignment{{UserID: &gone}, {UserID: &alive}},
	})
	require.NoError(t, err)

	stored, err := h.store.GetByID(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Equal(t, &alive, stored.AssigneeUserID, "the departed colleague is skipped")
	require.Equal(t, 1, h.atomic.commits, "ticket and routing are one transaction")
	require.Len(t, h.dispatcher.events, 1)
	ev := h.dispatcher.events[0]
	require.Equal(t, []notification.EventType{notification.EventTicketCreated, notification.EventTicketAssigned}, ev.Kinds)
	require.Equal(t, &alive, ev.Ticket.AssigneeUserID)
	// The routing is the system's act, as it was: its own audit entry, no actor.
	var routed int
	for _, e := range h.auditStore.entries {
		if e.Action == "assigned" {
			routed++
			require.Nil(t, e.ActorID)
		}
	}
	require.Equal(t, 1, routed)
}

// Nobody on the list can take it: the ticket is created unassigned, as before.
func TestCreate_NoAcceptableCandidateLeavesItUnassigned(t *testing.T) {
	h := newHarness(t)
	gone := uuid.New()
	h.store.notPeople = map[uuid.UUID]bool{gone: true}

	tk, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "s", CategoryID: uuid.New(), ReporterUserID: ptr(uuid.New()),
		AutoAssign: []ticket.Assignment{{UserID: &gone}},
	})
	require.NoError(t, err)
	require.Nil(t, tk.AssigneeUserID)
	require.Equal(t, []notification.EventType{notification.EventTicketCreated}, h.dispatcher.events[0].Kinds)
}

// Ruling 1, and the PATCH consolidation: a PATCH is all or nothing. A refused
// step later in the request leaves the earlier steps unwritten and announces
// nothing; before #506 the status was already saved and announced.
func TestUpdate_AFailingStepRollsBackTheWholePatchAndAnnouncesNothing(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedMedium()
	notStaff := uuid.New()
	h.store.notPeople = map[uuid.UUID]bool{notStaff: true} // the assignment refuses this account

	_, err := h.svc.Update(context.Background(), tk.ID, ticket.Patch{
		StatusID: &h.inProgressStatus.ID, Assign: &ticket.Assignment{UserID: &notStaff},
	}, reopenStaff)
	require.ErrorIs(t, err, ticket.ErrValidation)

	got, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, tk.StatusID, got.StatusID, "the status change is rolled back with it")
	require.Empty(t, h.dispatcher.events, "a rolled-back change announces nothing")
	require.Equal(t, 1, h.atomic.rollbacks)
}

// The classification of an unchanged request announces nothing.
func TestUpdate_ReclassifyingToTheSameClassificationAnnouncesNothing(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedMedium()
	_, err := h.svc.Update(context.Background(), tk.ID,
		ticket.Patch{CTI: &ticket.Classification{CategoryID: tk.CategoryID}}, reopenStaff)
	require.NoError(t, err)
	require.Empty(t, h.dispatcher.events)
}

// Routing marks its assignment, so staff mail does not treat it as the
// creator's own action (ruling B).
func TestCreate_TheRoutedAssignmentIsMarkedAsRouting(t *testing.T) {
	h := newHarness(t)
	staff := uuid.New()
	_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "s", CategoryID: uuid.New(), ReporterUserID: &staff,
		AutoAssign: []ticket.Assignment{{UserID: &staff}},
	})
	require.NoError(t, err)
	require.True(t, h.dispatcher.events[0].RoutedAssignment)

	h2 := newHarness(t)
	_, err = h2.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb, Subject: "s", CategoryID: uuid.New(), ReporterUserID: &staff})
	require.NoError(t, err)
	require.False(t, h2.dispatcher.events[0].RoutedAssignment)
}

// A status change to the status the ticket already has announces nothing about
// status (CTO ruling 13; 1.2.x announced status_changed here, which the upgrade
// notes say).
func TestSameStatus_AnnouncesNothingAboutStatus(t *testing.T) {
	ctx := context.Background()
	assignee := uuid.New()

	t.Run("a re-resolve with new notes is resolved only", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := h.seedMedium()
		_, err := h.svc.Resolve(ctx, tk.ID, "first", reopenStaff)
		require.NoError(t, err)
		h.dispatcher.events = nil

		_, err = h.svc.Resolve(ctx, tk.ID, "second", reopenStaff)
		require.NoError(t, err)

		require.Len(t, h.dispatcher.events, 1)
		require.Equal(t, []notification.EventType{notification.EventTicketResolved}, h.dispatcher.events[0].Kinds)
		for _, c := range h.dispatcher.events[0].Changes {
			require.NotEqual(t, "status_id", c.Field)
		}
	})

	t.Run("a PATCH of the same status plus an assignee is assigned only", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := h.seedMedium()
		_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{
			StatusID: &tk.StatusID, Assign: &ticket.Assignment{UserID: &assignee},
		}, reopenStaff)
		require.NoError(t, err)

		require.Len(t, h.dispatcher.events, 1)
		require.Equal(t, []notification.EventType{notification.EventTicketAssigned}, h.dispatcher.events[0].Kinds)
		require.Empty(t, h.dispatcher.events[0].Payload["new_status_id"])
	})

	t.Run("a PATCH of the same status alone announces nothing", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := h.seedMedium()
		_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{StatusID: &tk.StatusID}, reopenStaff)
		require.NoError(t, err)
		require.Empty(t, h.dispatcher.events)
	})
}

// Routing's audit entry says what it did: unassigned, then assigned (F4). It was
// the same ticket map twice.
func TestCreate_TheRoutingAuditEntryRecordsUnassignedToAssigned(t *testing.T) {
	h := newHarness(t)
	assignee := uuid.New()
	_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "s", CategoryID: uuid.New(), ReporterUserID: ptr(uuid.New()),
		AutoAssign: []ticket.Assignment{{UserID: &assignee}},
	})
	require.NoError(t, err)

	var routed audit.Entry
	for _, e := range h.auditStore.entries {
		if e.Action == "assigned" {
			routed = e
		}
	}
	require.NotEmpty(t, routed.Action)
	require.Nil(t, routed.Before["assignee_user_id"])
	require.Equal(t, assignee.String(), routed.After["assignee_user_id"])
	require.NotEqual(t, routed.Before, routed.After)
}

// Repeating Resolved is not a status move, whichever door repeats it (CTO
// ruling 13). Only the Resolve button, which carries notes, is a resolution.
func TestSameResolved_IsNotAStatusMoveAtAnyDoorButTheResolveButton(t *testing.T) {
	ctx := context.Background()
	assignee := uuid.New()
	guest := "guest@example.test"
	resolvedGuestTicket := func(h *prioHarness) ticket.Ticket {
		tk := h.seedFinished("resolved", ticket.PriorityMedium)
		tk.ReporterUserID, tk.GuestEmail = nil, &guest
		h.store.seed(tk)
		return tk
	}

	t.Run("a PATCH of Resolved plus an assignee is assigned only, and mails and rotates nothing", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := resolvedGuestTicket(h)
		_, err := h.svc.Update(ctx, tk.ID, ticket.Patch{
			StatusID: &h.resolvedStatus.ID, Assign: &ticket.Assignment{UserID: &assignee},
		}, reopenStaff)
		require.NoError(t, err)

		require.Len(t, h.dispatcher.events, 1)
		ev := h.dispatcher.events[0]
		require.Equal(t, []notification.EventType{notification.EventTicketAssigned}, ev.Kinds)
		require.Empty(t, ev.Recipient, "the guest is not told it was resolved again")
		require.Empty(t, ev.GuestToken, "and their link is not rotated")
		require.False(t, ev.GuestLink)
	})

	t.Run("update_ticket_status (UpdateStatus) to the same Resolved announces nothing about status", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := resolvedGuestTicket(h)
		_, err := h.svc.UpdateStatus(ctx, tk.ID, h.resolvedStatus.ID, reopenStaff)
		require.NoError(t, err)
		require.Empty(t, h.dispatcher.events)
	})
}

// A reopen whose target is the status the ticket already has (Closed) has no
// status kind to announce; it must not panic the collector.
func TestReopen_ToTheSameStatusDoesNotPanicAndAnnouncesNoStatus(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("closed", ticket.PriorityMedium)
	h.policy = ticket.ReopenPolicyStaffAdmin

	require.NotPanics(t, func() {
		_, _ = h.svc.Reopen(context.Background(), tk.ID, h.closedStatus.ID, reopenStaff)
	})
	for _, ev := range h.dispatcher.events {
		for _, k := range ev.Kinds {
			require.Equal(t, notification.EventTicketPriorityChanged, k, "nothing about status")
		}
	}
}
