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

// #625, closed-ticket follow-up limits 1 to 7 (the domain half).

// The link row says whether the follow-up action made it. Only that marker
// counts as a requester's one follow-up (D21); a link someone made by hand is
// just a link.
func TestFollowUpLink_OnlyTheFollowUpActionMarksIt(t *testing.T) {
	ctx := context.Background()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("staff follow-up", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, staff)
		require.NoError(t, err)
		require.True(t, h.store.links[orig.ID][0].FollowUp)
	})
	t.Run("requester follow-up", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
		require.NoError(t, err)
		require.True(t, h.store.links[orig.ID][0].FollowUp)
	})
	t.Run("a link made by hand", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		other := h.seedOpen()
		require.NoError(t, h.svc.AddLink(ctx, orig.ID, other.ID, ticket.LinkParentChild, staff))
		require.False(t, h.store.links[orig.ID][0].FollowUp)
	})
}

// A hand-made link does not use up the one follow-up, and the follow-up the
// action made still does.
func TestRequesterFollowUp_OnlyAFollowUpLinkUsesUpTheOne(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	other := h.seedOpen()
	require.NoError(t, h.svc.AddLink(ctx, orig.ID, other.ID, ticket.LinkParentChild,
		ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}))

	_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
	require.NoError(t, err)
	_, err = h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
	require.ErrorIs(t, err, ticket.ErrFollowUpExists)
}

// A lost double-submit used to burn a tracking number: it was taken before the
// transaction and the loser was refused inside it. The number is now taken
// after the locked check, so a refusal leaves the sequence where it was.
func TestFollowUp_ARefusedRequestTakesNoTrackingNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("a requester who lost the race", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		h.store.forUpdateReads = 0
		h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
			if x.ID == orig.ID && h.store.forUpdateReads > 0 && len(h.store.links[orig.ID]) == 0 {
				h.store.links[orig.ID] = []ticket.TicketLink{{
					SourceTicketID: orig.ID, TargetTicketID: uuid.New(), LinkType: ticket.LinkParentChild, FollowUp: true,
				}}
			}
			return x
		}
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
		require.ErrorIs(t, err, ticket.ErrFollowUpExists)
		require.Zero(t, h.store.seq, "the loser must not consume a number")
	})

	t.Run("a ticket reopened under the follow-up", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		h.store.forUpdateReads = 0
		h.store.onRead = func(x ticket.Ticket) ticket.Ticket {
			if x.ID == orig.ID && h.store.forUpdateReads > 0 {
				x.StatusID = h.newStatus.ID
			}
			return x
		}
		_, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
		require.ErrorIs(t, err, ticket.ErrNotClosed)
		require.Zero(t, h.store.seq)
	})

	t.Run("a winner still gets the next number", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		got, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
		require.NoError(t, err)
		require.EqualValues(t, 1, h.store.seq)
		require.Contains(t, string(got.TrackingNumber), "-000001")
	})
}

// A transaction that Postgres retries runs its body again from the top, so the
// number has to be drawn inside the body to be drawn afresh on each attempt. A
// failure to draw one is therefore the transaction's failure, and rolls it back.
func TestCreate_TheTrackingNumberIsTakenInsideTheTransaction(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	h.store.errNextSeq = errStoreDown

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		SourceChannel: ticket.ChannelWeb, Subject: "x", ReporterUserID: &reporter})

	require.ErrorIs(t, err, errStoreDown)
	require.Equal(t, 1, h.atomic.rollbacks)
	require.Zero(t, h.store.creates)
}

// Item 4: a follow-up of a ticket whose reporter's account was deleted is its
// own refusal, not a generic validation error, and takes no number.
func TestCreateFollowUp_ADeletedReporterIsItsOwnRefusal(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	h.store.deletedUsers = map[uuid.UUID]bool{*orig.ReporterUserID: true}
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	_, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", ticket.ChannelWeb, staff)

	require.ErrorIs(t, err, ticket.ErrReporterDeleted)
	require.NotErrorIs(t, err, ticket.ErrValidation, "it is not a mistake in what the caller sent")
	require.Zero(t, h.store.seq)
	require.Len(t, h.store.tickets, 1)
}

// An ordinary create with an unknown reporter is still a validation error.
func TestCreate_AnUnknownReporterStaysAValidationError(t *testing.T) {
	h := newHarness(t)
	gone := uuid.New()
	h.store.deletedUsers = map[uuid.UUID]bool{gone: true}

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		SourceChannel: ticket.ChannelWeb, Subject: "x", ReporterUserID: &gone})

	require.ErrorIs(t, err, ticket.ErrValidation)
	require.NotErrorIs(t, err, ticket.ErrReporterDeleted)
}

// Item 1: the created event of a follow-up says so, and an ordinary ticket's
// does not; the mail is worded from this flag.
func TestCreate_OnlyAFollowUpRaisesAFollowUpEvent(t *testing.T) {
	ctx := context.Background()

	t.Run("staff follow-up of a guest ticket", func(t *testing.T) {
		h := newHarness(t)
		staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
		tk, _ := guestTicket(t, h)
		require.NoError(t, h.svc.Close(ctx, tk.ID, staff))
		h.dispatcher.events = nil
		got, err := h.svc.CreateFollowUp(ctx, tk.ID, "GHD", ticket.ChannelWeb, staff)
		require.NoError(t, err)
		ev := lastEventOfType(t, h, notification.EventTicketCreated)
		require.Equal(t, got.ID, ev.TicketID)
		require.True(t, ev.FollowUp)
	})
	t.Run("guest follow-up", func(t *testing.T) {
		h := newHarness(t)
		staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
		tk, _ := guestTicket(t, h)
		require.NoError(t, h.svc.Close(ctx, tk.ID, staff))
		h.dispatcher.events = nil
		_, err := h.svc.CreateRequesterFollowUp(ctx, tk.ID, "GHD", ticket.ChannelGuest, guestActor, openAlways)
		require.NoError(t, err)
		require.True(t, lastEventOfType(t, h, notification.EventTicketCreated).FollowUp)
	})
	t.Run("an ordinary ticket", func(t *testing.T) {
		h := newHarness(t)
		_, _ = guestTicket(t, h)
		require.False(t, lastEventOfType(t, h, notification.EventTicketCreated).FollowUp)
	})
}

// Item 6: what the ticket page and the guest page ask.
func TestFollowUpOf(t *testing.T) {
	ctx := context.Background()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("none yet", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		got, err := h.svc.FollowUpOf(ctx, orig.ID, seeAll)
		require.NoError(t, err)
		require.Nil(t, got)
	})
	t.Run("a hand-made link is not one", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		require.NoError(t, h.svc.AddLink(ctx, orig.ID, h.seedOpen().ID, ticket.LinkParentChild, staff))
		got, err := h.svc.FollowUpOf(ctx, orig.ID, seeAll)
		require.NoError(t, err)
		require.Nil(t, got)
	})
	t.Run("the follow-up", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		made, err := h.svc.CreateRequesterFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig), openAlways)
		require.NoError(t, err)
		got, err := h.svc.FollowUpOf(ctx, orig.ID, seeAll)
		require.NoError(t, err)
		require.Equal(t, &ticket.FollowUpRef{ID: made.ID, TrackingNumber: made.TrackingNumber}, got)
	})
	t.Run("the newest of several", func(t *testing.T) {
		h := newHarness(t)
		orig := closedWithEverything(t, h)
		first, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, staff)
		require.NoError(t, err)
		second, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, staff)
		require.NoError(t, err)
		// Equal timestamps would make the order a coin toss.
		f := h.store.tickets[first.ID]
		f.CreatedAt = time.Now().Add(-time.Hour)
		h.store.tickets[first.ID] = f
		got, err := h.svc.FollowUpOf(ctx, orig.ID, seeAll)
		require.NoError(t, err)
		require.Equal(t, second.ID, got.ID)
	})
}

func seeAll(ticket.Ticket) (bool, error) { return true, nil }

// The caller says which tickets the viewer may open. A follow-up link can point
// at somebody else's ticket (migration 000064 marked every parent_child link),
// and what the page is told must not name a ticket the viewer cannot see. The
// newest one they CAN see is the answer; none visible is the same as none.
func TestFollowUpOf_OnlyNamesATicketTheViewerMayOpen(t *testing.T) {
	ctx := context.Background()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	mine, err := h.svc.CreateFollowUp(ctx, orig.ID, "GHD", ticket.ChannelWeb, staff)
	require.NoError(t, err)
	foreign := h.seedOpen()
	require.NoError(t, h.store.CreateLink(ctx, ticket.TicketLink{
		SourceTicketID: orig.ID, TargetTicketID: foreign.ID, LinkType: ticket.LinkParentChild, FollowUp: true}))
	f := h.store.tickets[foreign.ID]
	f.CreatedAt = time.Now().Add(time.Hour) // newer than the real one
	h.store.tickets[foreign.ID] = f

	onlyMine := func(t ticket.Ticket) (bool, error) { return t.ID == mine.ID, nil }
	got, err := h.svc.FollowUpOf(ctx, orig.ID, onlyMine)
	require.NoError(t, err)
	require.Equal(t, mine.ID, got.ID, "the foreign link is skipped even though it is newer")

	got, err = h.svc.FollowUpOf(ctx, orig.ID, func(ticket.Ticket) (bool, error) { return false, nil })
	require.NoError(t, err)
	require.Nil(t, got, "nothing visible reads as no follow-up")

	_, err = h.svc.FollowUpOf(ctx, orig.ID, func(ticket.Ticket) (bool, error) { return false, errStoreDown })
	require.ErrorIs(t, err, errStoreDown)
}

// Item 5: an expired token is deleted, a live one is not.
func TestSweepExpiredGuestTokens(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	old, _ := guestTicket(t, h)
	h.store.expireGuestTokens(old.ID)
	h.dispatcher.events = nil // guestTicket expects to see only its own
	live, _ := guestTicket(t, h)

	n, err := h.svc.SweepExpiredGuestTokens(ctx)

	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Zero(t, h.store.guestTokenCount(old.ID))
	require.Equal(t, 1, h.store.guestTokenCount(live.ID))
}
