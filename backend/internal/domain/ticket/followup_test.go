package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #349: a follow-up is the way forward from a Closed ticket. These tests pin
// what it copies, what it must not, that it is linked, and that the original is
// left alone.

// closedWithEverything plants a closed ticket carrying every field a follow-up
// might copy, plus the things it must not: replies, an assignee.
func closedWithEverything(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	typeID, itemID := uuid.New(), uuid.New()
	assignee := uuid.New()
	reporter := uuid.New()
	closedAt := time.Now().Add(-time.Hour)
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "GHD-2026-000042",
		Subject:        "Printer on 3 is down",
		Description:    "Jams on every page",
		CategoryID:     uuid.New(),
		TypeID:         &typeID,
		ItemID:         &itemID,
		Priority:       ticket.PriorityHigh,
		StatusID:       h.closedStatus.ID,
		AssigneeUserID: &assignee,
		ReporterUserID: &reporter,
		ResolvedAt:     &closedAt,
		ClosedAt:       &closedAt,
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      closedAt,
	}
	h.store.seed(tk)
	h.store.replies[tk.ID] = []ticket.Reply{{ID: uuid.New(), TicketID: tk.ID, Body: "old answer", CreatedAt: closedAt}}
	return tk
}

func TestCreateFollowUp_CopiesTheDetailsAndLinksBack(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	got, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", staff)
	require.NoError(t, err)

	require.NotEqual(t, orig.ID, got.ID)
	require.NotEqual(t, orig.TrackingNumber, got.TrackingNumber, "a new tracking number")
	require.Equal(t, orig.Subject, got.Subject)
	require.Equal(t, orig.Description, got.Description)
	require.Equal(t, orig.CategoryID, got.CategoryID)
	require.Equal(t, orig.TypeID, got.TypeID)
	require.Equal(t, orig.ItemID, got.ItemID)
	require.Equal(t, orig.Priority, got.Priority)
	require.Equal(t, orig.ReporterUserID, got.ReporterUserID, "same requester")

	// Starts open, from scratch.
	require.Equal(t, h.newStatus.ID, got.StatusID)
	require.Nil(t, got.ClosedAt)
	require.Nil(t, got.ResolvedAt)
	require.Nil(t, got.AssigneeUserID, "the assignee is not copied")
	require.Empty(t, h.store.replies[got.ID], "replies are not copied")

	// Linked: the closed ticket is the parent, the follow-up its child.
	require.Equal(t, []ticket.TicketLink{{
		SourceTicketID: orig.ID, TargetTicketID: got.ID, LinkType: ticket.LinkParentChild,
	}}, h.store.links[orig.ID])
}

// The rules of any new ticket still apply: status history, an audit entry
// naming who made it, the "created" notification.
func TestCreateFollowUp_GoesThroughTheOrdinaryCreationPath(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	staffID := uuid.New()
	staff := ticket.Actor{UserID: &staffID, Role: user.RoleStaff}

	got, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", staff)
	require.NoError(t, err)

	hist := historyFor(h, got.ID)
	require.Len(t, hist, 1)
	require.Equal(t, &staffID, hist[0].ChangedByUserID, "the history names the member of staff, not the requester")

	var found bool
	for _, e := range h.auditStore.entries {
		if e.EntityID == got.ID && e.Action == "created" {
			found = true
			require.NotNil(t, e.ActorID)
			require.Equal(t, staffID, *e.ActorID, "the audit entry names the member of staff")
			require.Equal(t, orig.ID, e.After["follow_up_of"], "and says what this is a follow-up of")
		}
	}
	require.True(t, found, "an audit entry is written")

	require.Contains(t, h.dispatcher.types(), notification.EventTicketCreated)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketLinked)
	require.Equal(t, 1, h.atomic.commits, "ticket, history, audit and link are one transaction")
}

// The original is left exactly as it was: still Closed, same replies, same
// history, and a guest's links untouched.
func TestCreateFollowUp_LeavesTheOriginalUntouched(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, token := guestTicket(t, h)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	before, _ := h.store.GetByID(context.Background(), tk.ID)
	histBefore := len(historyFor(h, tk.ID))
	repliesBefore := len(h.store.replies[tk.ID])
	tokensBefore := h.store.guestTokenCount(tk.ID)
	updatesBefore := h.store.updates

	_, err := h.svc.CreateFollowUp(context.Background(), tk.ID, "GHD", staff)
	require.NoError(t, err)

	after, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, before, after, "the closed ticket's row is unchanged")
	require.Equal(t, updatesBefore, h.store.updates, "and was not written")
	require.Equal(t, histBefore, len(historyFor(h, tk.ID)))
	require.Equal(t, repliesBefore, len(h.store.replies[tk.ID]))
	require.Equal(t, tokensBefore, h.store.guestTokenCount(tk.ID), "the original's link tokens are untouched")
	_, err = h.svc.TicketForGuestToken(context.Background(), token)
	require.NoError(t, err, "the guest's link to the archive still reads")
}

// A guest's follow-up copies the guest as the requester, and goes through the
// same notification as a new guest ticket: they are told, with a link to it.
func TestCreateFollowUp_ForAGuestTicketCopiesTheGuest(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	h.dispatcher.events = nil

	got, err := h.svc.CreateFollowUp(context.Background(), tk.ID, "GHD", staff)
	require.NoError(t, err)

	require.Nil(t, got.ReporterUserID)
	require.NotNil(t, got.GuestEmail)
	require.Equal(t, "guest@example.test", *got.GuestEmail)
	require.Equal(t, "Ada", got.GuestName)
	ev := lastEventOfType(t, h, notification.EventTicketCreated)
	require.Equal(t, got.ID, ev.TicketID)
	require.NotEmpty(t, ev.GuestToken, "the guest is mailed a link to the new ticket")
	require.Equal(t, "guest@example.test", ev.Recipient)
}

func TestCreateFollowUp_Refusals(t *testing.T) {
	ctx := context.Background()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("a requester may not", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		creates := h.store.creates
		_, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD",
			ticket.Actor{UserID: orig.ReporterUserID, Role: user.RoleUser})
		require.ErrorIs(t, err, ticket.ErrForbidden)
		require.Equal(t, creates, h.store.creates)
		require.Empty(t, h.store.links[orig.ID])
	})

	// Decision: a follow-up continues a CLOSED ticket. An open or resolved one
	// is refused (409), so this is not a general "duplicate this ticket".
	for name, seed := range map[string]func(h *harness) ticket.Ticket{
		"an open ticket":    func(h *harness) ticket.Ticket { return h.seedOpen() },
		"a resolved ticket": func(h *harness) ticket.Ticket { return h.seedResolved(uuid.New()) },
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			h := newHarness(t)
			orig := seed(h)
			creates := h.store.creates
			_, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", staff)
			require.ErrorIs(t, err, ticket.ErrNotClosed)
			require.Equal(t, creates, h.store.creates, "no ticket is created, so no tracking number is taken")
		})
	}

	t.Run("a missing ticket is not found", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.svc.CreateFollowUp(ctx, uuid.New(), "GHD", staff)
		require.ErrorIs(t, err, errNotFound)
	})

	t.Run("a failed link rolls the follow-up back", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		h.store.errCreateLink = errStoreDown
		_, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", staff)
		require.ErrorIs(t, err, errStoreDown)
		require.Equal(t, 1, h.atomic.rollbacks)
		require.Len(t, h.store.tickets, 1, "only the original remains")
	})
}

// A follow-up of a follow-up works: the second is linked to the first, which is
// itself closed.
func TestCreateFollowUp_CanContinueAChain(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	orig := closedWithEverything(t, h)
	first, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", staff)
	require.NoError(t, err)
	require.NoError(t, h.svc.Close(context.Background(), first.ID, staff))

	second, err := h.svc.CreateFollowUp(context.Background(), first.ID, "GHD", staff)
	require.NoError(t, err)
	require.Equal(t, second.ID, h.store.links[first.ID][0].TargetTicketID)
}
