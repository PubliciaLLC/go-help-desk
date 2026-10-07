package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #349: Closed is TERMINAL by default (closed_reopen_policy off), for every
// role. These tests are the rule under that default — the harness's policy is
// empty, which reads as off — and each of the doors out of Closed is one case.
// The "on" half, and the requesters-never half, are in
// closed_reopen_policy_test.go.
//
// Before #349 an administrator could Reopen a closed ticket, and staff could
// move one out of Closed with a status change or resolve it again. All of
// those are refused by default now: the way forward from a closed ticket is a
// new, linked ticket (CreateFollowUp), unless an operator turns forced reopen
// on (closed_reopen_policy).

func terminalActors() map[string]ticket.Actor {
	id := uuid.New()
	return map[string]ticket.Actor{
		"staff":  {UserID: &id, Role: user.RoleStaff},
		"admin":  {UserID: &id, Role: user.RoleAdmin},
		"system": ticket.SystemActor,
	}
}

func TestClosedIsTerminal_NoStatusChangeLeavesIt(t *testing.T) {
	for name, actor := range terminalActors() {
		for _, target := range []string{"New", "Resolved", "In Progress", "Pending"} {
			t.Run(name+" to "+target, func(t *testing.T) {
				h := newHarness(t)
				seeded := h.seedClosed()

				_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.statusNamed(target).ID, actor)

				require.ErrorIs(t, err, ticket.ErrClosed)
				stored, getErr := h.store.GetByID(context.Background(), seeded.ID)
				require.NoError(t, getErr)
				require.Equal(t, h.closedStatus.ID, stored.StatusID, "the ticket must still be Closed")
				require.NotNil(t, stored.ClosedAt)
				require.Zero(t, h.store.updates, "nothing may be written")
				require.Empty(t, h.dispatcher.types(), "nothing may be announced")
			})
		}
	}
}

func TestClosedIsTerminal_ResolveDoesNotLeaveIt(t *testing.T) {
	for name, actor := range terminalActors() {
		t.Run(name+" Resolve", func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedClosed()

			_, err := h.svc.Resolve(context.Background(), seeded.ID, "again", actor)

			require.ErrorIs(t, err, ticket.ErrClosed)
			stored, _ := h.store.GetByID(context.Background(), seeded.ID)
			require.Equal(t, h.closedStatus.ID, stored.StatusID)
			require.Zero(t, h.store.updates)
		})
		t.Run(name+" ResolveAsDuplicate", func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedClosed()
			target := h.seedOpen()

			_, err := h.svc.ResolveAsDuplicate(context.Background(), seeded.ID, target.ID, "dup", actor)

			require.ErrorIs(t, err, ticket.ErrClosed)
			stored, _ := h.store.GetByID(context.Background(), seeded.ID)
			require.Equal(t, h.closedStatus.ID, stored.StatusID)
			require.Empty(t, h.store.links[seeded.ID], "no link may be written either")
		})
	}
}

// Closing a closed ticket again is not a way out and not an error.
func TestClosedIsTerminal_CloseIsIdempotent(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedClosed()
	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))
	require.Zero(t, h.store.updates)
}

// The reply path's auto-reopen is Resolved-only, so it cannot reopen a closed
// ticket — and a requester's reply to a closed ticket is refused, not
// accepted-and-reopened. The ticket stays Closed.
func TestClosedIsTerminal_ARequesterReplyDoesNotReopenIt(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedClosed()

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "still broken", false, true, "r@example.test",
		ticket.Actor{UserID: seeded.ReporterUserID, Role: user.RoleUser}, 7, h.newStatus.ID)

	require.ErrorIs(t, err, ticket.ErrClosed)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
	require.Empty(t, h.store.replies[seeded.ID])
}

// The races. A requester's write is decided on a read that is not the write:
// the check reads the ticket, the insert comes later, and a close can commit
// between them. The fake store's onRead hook plays that interleaving
// deterministically: an UNLOCKED read (forUpdateReads == 0) still sees the
// ticket as it was, and a read under the row lock — the one the write must
// take — sees the close that committed first.
//
// The rule these pin: "Closed is read-only for requesters" holds against a
// close that wins the race, not only against one that was already committed.
// Before the check moved onto the locked row, a reply that lost the race
// STOOD, on a Closed ticket (the old TestClosedIsTerminal_AReplyRacingTheCloseDoesNotReopen
// pinned exactly that: "the reply stands, the reopen does not").
func closeBetweenCheckAndWrite(h *harness, id uuid.UUID) {
	// Earlier setup may already have taken locks (a guest ticket's creation
	// does), which would make the very first, unlocked read look locked.
	h.store.forUpdateReads = 0
	h.store.onRead = func(tk ticket.Ticket) ticket.Ticket {
		if tk.ID == id && h.store.forUpdateReads > 0 {
			tk.StatusID = h.closedStatus.ID
		}
		return tk
	}
}

func TestClosedIsTerminal_AReplyRacingTheCloseIsRefusedAndReopensNothing(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	seeded := h.seedResolved(reporter) // would reopen: Resolved, inside the window
	closeBetweenCheckAndWrite(h, seeded.ID)

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "still broken", false, true, "r@example.test",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser}, 7, h.newStatus.ID)

	require.ErrorIs(t, err, ticket.ErrClosed, "the reply lost the race to the close")
	require.Empty(t, h.store.replies[seeded.ID], "and must not have been written")
	require.NotContains(t, h.dispatcher.types(), notification.EventTicketReopened)
	require.Zero(t, h.store.updates, "the row must not be rewritten out of Closed")
	require.Equal(t, 1, h.atomic.rollbacks)
}

func TestClosedIsTerminal_ARequesterReplyRacingTheCloseOnAnOpenTicketIsRefused(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen() // not Resolved: nothing to reopen, only a reply to land
	closeBetweenCheckAndWrite(h, seeded.ID)

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "hello", false, false, "",
		ticket.Actor{UserID: seeded.ReporterUserID, Role: user.RoleUser}, 7, h.newStatus.ID)

	require.ErrorIs(t, err, ticket.ErrClosed)
	require.Empty(t, h.store.replies[seeded.ID])
}

func TestClosedIsTerminal_AGuestReplyRacingTheCloseIsRefused(t *testing.T) {
	h := newHarness(t)
	tk, _ := guestTicket(t, h)
	closeBetweenCheckAndWrite(h, tk.ID)

	_, err := h.svc.AddGuestReply(context.Background(), tk.ID, "late", 7, h.newStatus.ID)

	require.ErrorIs(t, err, ticket.ErrClosed, "the handler maps this to the generic 404")
	require.Empty(t, h.store.replies[tk.ID])
}

// An attachment's row is inserted long after the check (the upload is read,
// scanned and written to disk in between), so the window is wider than for a
// reply. The insert re-decides under the lock.
func TestClosedIsTerminal_AnAttachmentRacingTheCloseIsRefused(t *testing.T) {
	reporter := uuid.New()
	for name, actor := range map[string]ticket.Actor{
		"reporter": {UserID: &reporter, Role: user.RoleUser},
		"guest":    {Role: user.RoleUser},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedOpen()
			closeBetweenCheckAndWrite(h, seeded.ID)

			err := h.svc.CreateAttachment(context.Background(),
				ticket.Attachment{ID: uuid.New(), TicketID: seeded.ID, Filename: "a.txt"}, actor)

			require.ErrorIs(t, err, ticket.ErrClosed)
			require.Zero(t, h.store.attachmentCreates, "no row for a closed ticket")
		})
	}
}

// Staff are not asked: the race is a requester's rule, and staff may still
// annotate a closed ticket (decision recorded in DESIGN.md).
func TestClosedIsTerminal_StaffReplyAndAttachmentOnAClosedTicketStand(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedClosed()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "for the record", true, false, "", staff, 7, h.newStatus.ID)
	require.NoError(t, err)
	require.NoError(t, h.svc.CreateAttachment(context.Background(),
		ticket.Attachment{ID: uuid.New(), TicketID: seeded.ID, Filename: "fix.txt"}, staff))
	require.Equal(t, 1, h.store.attachmentCreates)
}

// And a requester on an OPEN ticket is unaffected: the lock is taken, nothing
// is refused.
func TestRequesterWrites_OnAnOpenTicketStillLand(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	a := ticket.Actor{UserID: seeded.ReporterUserID, Role: user.RoleUser}

	_, err := h.svc.AddReply(context.Background(), seeded.ID, "hello", false, false, "", a, 7, h.newStatus.ID)
	require.NoError(t, err)
	require.NoError(t, h.svc.CreateAttachment(context.Background(),
		ticket.Attachment{ID: uuid.New(), TicketID: seeded.ID, Filename: "a.txt"}, a))
	require.Equal(t, 1, h.store.attachmentCreates)
}

// The minimal reading of the rule (decision recorded in DESIGN.md): only
// leaving Closed is removed for staff and admin. Everything else they could do
// to a closed ticket they still can.
func TestClosedIsTerminal_StaffKeepTheirOtherActions(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("reply and internal note", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		_, err := h.svc.AddReply(context.Background(), seeded.ID, "for the record", false, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)
		_, err = h.svc.AddReply(context.Background(), seeded.ID, "note", true, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)
		stored, _ := h.store.GetByID(context.Background(), seeded.ID)
		require.Equal(t, h.closedStatus.ID, stored.StatusID, "and the reply does not reopen it")
	})

	t.Run("assignment", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		assignee := uuid.New()
		_, err := h.svc.Assign(context.Background(), seeded.ID, &assignee, nil, staff)
		require.NoError(t, err)
	})

	t.Run("link", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		other := h.seedOpen()
		require.NoError(t, h.svc.AddLink(context.Background(), seeded.ID, other.ID, ticket.LinkRelatedTo, staff))
	})
}

// The requester's side: one rule, asked by every write path that is not a
// reply. A reporting user may read their closed ticket and change nothing.
func TestRequesterWrite_ClosedIsReadOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("a reporter is refused on a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		err := h.svc.CanRequesterWrite(ctx, seeded.ID,
			ticket.Actor{UserID: seeded.ReporterUserID, Role: user.RoleUser})
		require.ErrorIs(t, err, ticket.ErrClosed)
	})

	t.Run("a reporter is not refused on an open or resolved one", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		open := h.seedOpen()
		resolved := h.seedResolved(reporter)
		a := ticket.Actor{UserID: &reporter, Role: user.RoleUser}
		require.NoError(t, h.svc.CanRequesterWrite(ctx, open.ID, a))
		require.NoError(t, h.svc.CanRequesterWrite(ctx, resolved.ID, a))
	})

	t.Run("staff and admin are not refused on a closed one", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
			require.NoError(t, h.svc.CanRequesterWrite(ctx, seeded.ID,
				ticket.Actor{UserID: ptr(uuid.New()), Role: role}), string(role))
		}
	})

	t.Run("a missing ticket is not found", func(t *testing.T) {
		h := newHarness(t)
		err := h.svc.CanRequesterWrite(ctx, uuid.New(), ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleUser})
		require.ErrorIs(t, err, errNotFound)
	})
}

// AddLink is a write on BOTH tickets' threads, so a requester is refused when
// either end is closed.
func TestAddLink_ARequesterCannotLinkToOrFromAClosedTicket(t *testing.T) {
	ctx := context.Background()
	reporter := uuid.New()
	a := ticket.Actor{UserID: &reporter, Role: user.RoleUser}

	for name, build := range map[string]func(h *harness) (src, dst ticket.Ticket){
		"closed source": func(h *harness) (ticket.Ticket, ticket.Ticket) { return h.seedClosed(), h.seedOpen() },
		"closed target": func(h *harness) (ticket.Ticket, ticket.Ticket) { return h.seedOpen(), h.seedClosed() },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			src, dst := build(h)
			err := h.svc.AddLink(ctx, src.ID, dst.ID, ticket.LinkRelatedTo, a)
			require.ErrorIs(t, err, ticket.ErrClosed)
			require.Empty(t, h.store.links[src.ID])
		})
	}

	t.Run("both open is unchanged", func(t *testing.T) {
		h := newHarness(t)
		require.NoError(t, h.svc.AddLink(ctx, h.seedOpen().ID, h.seedOpen().ID, ticket.LinkRelatedTo, a))
	})
}
