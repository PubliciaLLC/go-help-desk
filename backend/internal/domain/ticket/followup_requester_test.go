package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #349: a REQUESTER may open a follow-up of their own closed ticket too.
//
// The principle: a requester's follow-up must not be a ticket the requester
// could not have created directly. So it copies only what a requester may set
// when creating one — subject, description, category (and type, for an account
// holder; never an item, never for a guest), and who they are — and takes
// every staff-controlled field from the defaults a normal create gives:
// medium priority, unassigned, no tags, a fresh SLA clock. Staff-initiated
// follow-ups keep the broader copy set (followup_test.go).

var openAlways = func(context.Context, uuid.UUID, *uuid.UUID) error { return nil }

func reporterActor(tk ticket.Ticket) ticket.Actor {
	return ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}
}

var guestActor = ticket.Actor{Role: user.RoleUser}

func TestRequesterFollowUp_AnAccountHolderCopiesOnlyWhatTheyMaySet(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h) // priority high, assignee, type AND item, replies

	got, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig), openAlways)
	require.NoError(t, err)

	require.NotEqual(t, orig.ID, got.ID)
	require.Equal(t, orig.Subject, got.Subject)
	require.Equal(t, orig.Description, got.Description)
	require.Equal(t, orig.CategoryID, got.CategoryID)
	require.Equal(t, orig.TypeID, got.TypeID, "an account holder picks category and type")
	require.Nil(t, got.ItemID, "but not an item")
	require.Equal(t, ticket.PriorityMedium, got.Priority, "the priority staff raised is not theirs to carry over")
	require.Equal(t, orig.ReporterUserID, got.ReporterUserID, "owned by them")
	require.Nil(t, got.AssigneeUserID)
	require.Nil(t, got.AssigneeGroupID)
	require.Equal(t, h.newStatus.ID, got.StatusID)
	require.Empty(t, h.store.replies[got.ID])

	// Linked, the closed ticket the parent, in the same transaction.
	require.Equal(t, []ticket.TicketLink{{
		SourceTicketID: orig.ID, TargetTicketID: got.ID, LinkType: ticket.LinkParentChild,
	}}, h.store.links[orig.ID])
	require.Equal(t, 1, h.atomic.commits)
}

func TestRequesterFollowUp_AGuestCopiesOnlyWhatAGuestMaySet(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)
	// Staff raised it and filed it under a type and item: not a guest's to set.
	stored, _ := h.store.GetByID(context.Background(), tk.ID)
	typeID, itemID := uuid.New(), uuid.New()
	stored.Priority, stored.TypeID, stored.ItemID = ticket.PriorityCritical, &typeID, &itemID
	h.store.seed(stored)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	h.dispatcher.events = nil
	h.dispatcher.sendTime = nil // queue only: what the outbox would hold

	got, err := h.svc.CreateRequesterFollowUp(context.Background(), tk.ID, "GHD", guestActor, openAlways)
	require.NoError(t, err)

	require.Nil(t, got.ReporterUserID)
	require.Equal(t, "guest@example.test", *got.GuestEmail)
	require.Equal(t, "Ada", got.GuestName)
	require.Equal(t, stored.CategoryID, got.CategoryID)
	require.Nil(t, got.TypeID, "a guest picks a category only")
	require.Nil(t, got.ItemID)
	require.Equal(t, ticket.PriorityMedium, got.Priority)
	require.Nil(t, got.AssigneeUserID)

	// A new guest ticket through the ordinary path: its link goes by mail, as a
	// normal guest ticket's does.
	ev := lastEventOfType(t, h, notification.EventTicketCreated)
	require.Equal(t, got.ID, ev.TicketID)
	require.True(t, ev.GuestLink)
	require.Equal(t, "guest@example.test", ev.Recipient)
}

func TestRequesterFollowUp_AuditNamesTheRequesterAndWhatItContinues(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	got, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig), openAlways)
	require.NoError(t, err)

	hist := historyFor(h, got.ID)
	require.Len(t, hist, 1)
	require.Equal(t, orig.ReporterUserID, hist[0].ChangedByUserID)
	var found bool
	for _, e := range h.auditStore.entries {
		if e.EntityID == got.ID && e.Action == "created" {
			found = true
			require.Equal(t, orig.ReporterUserID, e.ActorID)
			require.Equal(t, orig.ID, e.After["follow_up_of"])
		}
	}
	require.True(t, found)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketCreated, "staff hear about it as of any new ticket")
}

func TestRequesterFollowUp_Refusals(t *testing.T) {
	ctx := context.Background()

	t.Run("someone else's ticket", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		creates := h.store.creates
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD",
			ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleUser}, openAlways)
		require.ErrorIs(t, err, ticket.ErrForbidden)
		require.Equal(t, creates, h.store.creates, "no tracking number is taken")
	})

	t.Run("a guest cannot follow up an account holder's ticket", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", guestActor, openAlways)
		require.ErrorIs(t, err, ticket.ErrForbidden)
	})

	t.Run("an account holder cannot follow up a guest's ticket", func(t *testing.T) {
		h := newHarness(t)
		tk, _ := guestTicket(t, h)
		require.NoError(t, h.svc.Close(ctx, tk.ID, ticket.SystemActor))
		_, err := h.svc.CreateRequesterFollowUp(ctx, tk.ID, "GHD",
			ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleUser}, openAlways)
		require.ErrorIs(t, err, ticket.ErrForbidden)
	})

	for name, seed := range map[string]func(h *harness, reporter uuid.UUID) ticket.Ticket{
		"an open ticket":    func(h *harness, r uuid.UUID) ticket.Ticket { return h.seedOpenTicket(r) },
		"a resolved ticket": func(h *harness, r uuid.UUID) ticket.Ticket { return h.seedResolved(r) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			reporter := uuid.New()
			orig := seed(h, reporter)
			creates, seq := h.store.creates, h.store.seq
			_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD",
				ticket.Actor{UserID: &reporter, Role: user.RoleUser}, openAlways)
			require.ErrorIs(t, err, ticket.ErrNotClosed)
			require.Equal(t, creates, h.store.creates)
			require.Equal(t, seq, h.store.seq, "refused before a tracking number is taken")
		})
	}

	t.Run("a missing ticket", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.svc.CreateRequesterFollowUp(ctx, uuid.New(), "GHD", guestActor, openAlways)
		require.ErrorIs(t, err, errNotFound)
	})

	t.Run("staff and admin use the other method", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD",
			ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}, openAlways)
		require.ErrorIs(t, err, ticket.ErrForbidden, "a requester method for requesters")
	})
}

// The classification must still be open to a requester, as for a normal create:
// the caller supplies that rule, and its refusal stops the follow-up before a
// tracking number is taken.
func TestRequesterFollowUp_TheCreationRulesOfTheRoleApply(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	errArchived := errors.New("category_id is not an active category")
	creates := h.store.creates

	var sawType *uuid.UUID
	_, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig),
		func(_ context.Context, cat uuid.UUID, ty *uuid.UUID) error {
			require.Equal(t, orig.CategoryID, cat)
			sawType = ty
			return errArchived
		})

	require.ErrorIs(t, err, ticket.ErrValidation)
	require.ErrorContains(t, err, "not an active category")
	require.Equal(t, orig.TypeID, sawType, "asked about the type an account holder would carry")
	require.Equal(t, creates, h.store.creates)
	require.Empty(t, h.store.links[orig.ID])
}

// One follow-up per closed ticket for a requester: staff-controlled follow-ups
// are unlimited, a requester's is not, so a button cannot be turned into a
// stream of tickets. The cap is checked before a tracking number is taken.
func TestRequesterFollowUp_OnePerClosedTicket(t *testing.T) {
	ctx := context.Background()

	t.Run("a second one is refused", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)
		require.NoError(t, err)
		creates, seq := h.store.creates, h.store.seq

		_, err = h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)

		require.ErrorIs(t, err, ticket.ErrFollowUpExists)
		require.Equal(t, creates, h.store.creates)
		require.Equal(t, seq, h.store.seq, "and no tracking number is taken")
		require.Len(t, h.store.links[orig.ID], 1)
	})

	t.Run("a follow-up staff opened counts", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff})
		require.NoError(t, err)
		_, err = h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)
		require.ErrorIs(t, err, ticket.ErrFollowUpExists)
	})

	// The conservative reading, pinned so that changing it is a decision: the
	// cap cannot tell a follow-up from a parent_child link someone made by hand.
	t.Run("a hand-made parent link counts", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		other := h.seedOpen()
		require.NoError(t, h.svc.AddLink(ctx, orig.ID, other.ID, ticket.LinkParentChild,
			ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}))

		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)

		require.ErrorIs(t, err, ticket.ErrFollowUpExists)
	})

	t.Run("other links do not count", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		other := h.seedOpen()
		staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
		require.NoError(t, h.svc.AddLink(ctx, orig.ID, other.ID, ticket.LinkRelatedTo, staff))
		require.NoError(t, h.svc.AddLink(ctx, other.ID, orig.ID, ticket.LinkParentChild, staff)) // orig is the child

		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)

		require.NoError(t, err)
	})

	t.Run("staff are not limited", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)
		require.NoError(t, err)
		_, err = h.svc.CreateFollowUp(ctx, orig.ID, "GHD", staff)
		require.NoError(t, err)
		_, err = h.svc.CreateFollowUp(ctx, orig.ID, "GHD", staff)
		require.NoError(t, err)
	})

	t.Run("a race on the locked row is caught", func(t *testing.T) {
		// The unlocked pre-check sees no follow-up; the locked re-check sees the
		// one a concurrent request just wrote.
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		h.store.forUpdateReads = 0
		h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
			if x.ID == orig.ID && h.store.forUpdateReads > 0 && len(h.store.links[orig.ID]) == 0 {
				h.store.links[orig.ID] = []ticket.TicketLink{{
					SourceTicketID: orig.ID, TargetTicketID: uuid.New(), LinkType: ticket.LinkParentChild,
				}}
			}
			return x
		}
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", reporterActor(orig), openAlways)
		require.ErrorIs(t, err, ticket.ErrFollowUpExists)
		require.Len(t, h.store.tickets, 1, "nothing was created")
	})
}

// A ticket forced open between the check and the lock is no longer one to
// follow up: the locked original is decided again.
func TestRequesterFollowUp_AReopenRacingTheFollowUpIsRefused(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	h.store.forUpdateReads = 0
	h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
		if x.ID == orig.ID && h.store.forUpdateReads > 0 {
			x.StatusID = h.newStatus.ID
		}
		return x
	}

	_, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig), openAlways)

	require.ErrorIs(t, err, ticket.ErrNotClosed)
	require.Len(t, h.store.tickets, 1)
	require.Empty(t, h.store.links[orig.ID])
}

// The original is untouched: status, replies, history, guest links.
func TestRequesterFollowUp_LeavesTheOriginalUntouched(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, token := guestTicket(t, h)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	before, _ := h.store.GetByID(context.Background(), tk.ID)
	histBefore := len(historyFor(h, tk.ID))
	tokensBefore := h.store.guestTokenCount(tk.ID)
	updates := h.store.updates

	_, err := h.svc.CreateRequesterFollowUp(context.Background(), tk.ID, "GHD", guestActor, openAlways)
	require.NoError(t, err)

	after, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, before, after)
	require.Equal(t, updates, h.store.updates)
	require.Equal(t, histBefore, len(historyFor(h, tk.ID)))
	require.Equal(t, tokensBefore, h.store.guestTokenCount(tk.ID))
	_, err = h.svc.TicketForGuestToken(context.Background(), token)
	require.NoError(t, err)
}

func TestRequesterFollowUp_AFailedLinkRollsTheTicketBack(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	h.store.errCreateLink = errStoreDown

	_, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig), openAlways)

	require.ErrorIs(t, err, errStoreDown)
	require.Equal(t, 1, h.atomic.rollbacks)
	require.Len(t, h.store.tickets, 1)
}

// What a requester's follow-up does not depend on: the reopen policy.
func TestRequesterFollowUp_IsAvailableInEveryPolicyMode(t *testing.T) {
	for _, policy := range []string{"", ticket.ReopenPolicyAdmin, ticket.ReopenPolicyStaffAdmin} {
		h := newHarness(t)
		h.policy = policy
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", reporterActor(orig), openAlways)
		require.NoError(t, err, policy)
	}
}
