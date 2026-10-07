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

func guestTicket(t *testing.T, h *harness) (ticket.Ticket, string) {
	t.Helper()
	email := "guest@example.test"
	tk, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer broken", Description: "it is", CategoryID: uuid.New(),
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	require.Len(t, h.dispatcher.events, 1)
	tokenOf := h.dispatcher.events[0].GuestToken
	require.NotEmpty(t, tokenOf, "creating a guest ticket must mint a link")
	return tk, tokenOf
}

// The token is the guest's whole credential, so the first question is whether
// it reaches the one ticket it names and nothing else.
func TestGuestToken_ReachesItsOwnTicketAndNoOther(t *testing.T) {
	h := newHarness(t)
	tk, token := guestTicket(t, h)

	got, err := h.svc.TicketForGuestToken(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, tk.ID, got.ID)

	_, err = h.svc.TicketForGuestToken(context.Background(), token+"x")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)

	_, err = h.svc.TicketForGuestToken(context.Background(), "")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
}

// The raw token must never be what is stored. A test that looked a token up by
// its raw value would not notice the day the hashing stopped.
func TestGuestToken_IsStoredHashedNotRaw(t *testing.T) {
	h := newHarness(t)
	_, token := guestTicket(t, h)

	require.NotContains(t, h.store.guestTokens, token,
		"the raw token must not be a key in the store")
	require.Len(t, h.store.guestTokens, 1)
	for hash := range h.store.guestTokens {
		require.NotEqual(t, token, hash)
		require.Len(t, hash, 64, "sha-256, hex encoded")
	}
}

// A ticket with a reporter account has nobody to send a link to, so minting one
// would be a credential issued for no reason.
func TestGuestToken_NotMintedForAnAccountHoldersTicket(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "Laptop", Description: "broken", CategoryID: uuid.New(),
		ReporterUserID: &reporter,
	})
	require.NoError(t, err)
	require.Empty(t, h.dispatcher.events[0].GuestToken)
	require.Empty(t, h.store.guestTokens)
}

// Decision 2: which updates rotate. A staff reply, a status change, a
// resolution and a reopen each replace the link and carry the replacement. An
// internal note does not — rotating would lock the guest out, and the email
// announcing a new link would disclose that staff had written privately about
// their ticket.
func TestGuestToken_RotatesOnlyOnWhatTheGuestIsTold(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("a public reply rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.AddReply(context.Background(), tk.ID, "looking into it",
			false, true, "guest@example.test", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketReplied)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken, "the link must change")
		require.Equal(t, 1, h.store.guestTokenCount(tk.ID), "rotation replaces, it does not accumulate")

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "no grace period")
		_, err = h.svc.TicketForGuestToken(context.Background(), ev.GuestToken)
		require.NoError(t, err)
	})

	t.Run("an internal note does not rotate", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.AddReply(context.Background(), tk.ID, "cost code 4471",
			true, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketReplied)
		require.Empty(t, ev.GuestToken, "an internal note must mint nothing")
		require.Empty(t, ev.Recipient, "and must reach nobody")

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.NoError(t, err, "the guest's existing link must keep working")
	})

	t.Run("a status change rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.resolvedStatus.ID, staff)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketStatusChanged)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken)
		require.Equal(t, "guest@example.test", ev.Recipient)
	})

	t.Run("a resolution rotates", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		_, err := h.svc.Resolve(context.Background(), tk.ID, "replaced the drum", staff)
		require.NoError(t, err)

		ev := lastEventOfType(t, h, notification.EventTicketResolved)
		require.NotEmpty(t, ev.GuestToken)
		require.NotEqual(t, first, ev.GuestToken)
	})

	t.Run("an assignment does not rotate", func(t *testing.T) {
		h := newHarness(t)
		tk, first := guestTicket(t, h)
		assignee := uuid.New()
		_, err := h.svc.Assign(context.Background(), tk.ID, &assignee, nil, staff)
		require.NoError(t, err)

		_, err = h.svc.TicketForGuestToken(context.Background(), first)
		require.NoError(t, err, "internal bookkeeping must not lock the guest out")
	})
}

// Closing STOPS ROTATING the link; it does not revoke it (#349).
//
// This test used to pin the opposite — "closing leaves no token" — because a
// closed ticket was thought to need no reader. Erik's rule: a closed ticket is
// an archive the requester may still READ, so the last link sent keeps working
// until it expires (GuestTokenTTL). There is no "reopening issues afresh"
// half any more: Closed is terminal for every role, so a closed ticket never
// comes back and never needs a way back in.
func TestGuestToken_ClosingKeepsTheLinkForReading(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, first := guestTicket(t, h)

	require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))
	require.Equal(t, 1, h.store.guestTokenCount(tk.ID), "closing neither deletes nor replaces the token")

	got, err := h.svc.TicketForGuestToken(context.Background(), first)
	require.NoError(t, err, "the link sent before the close must still read the ticket")
	require.Equal(t, tk.ID, got.ID)
}

// A closed ticket is readable through its link and writable through none: the
// write lookup refuses with the same error as a link that never existed, so
// the handler's refusal is the generic 404 and does not say the ticket exists.
func TestGuestToken_ClosedIsReadableAndNeverWritable(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, token := guestTicket(t, h)
	ctx := context.Background()

	_, err := h.svc.TicketForGuestWrite(ctx, token)
	require.NoError(t, err, "while the ticket is open the same link writes")

	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))

	_, err = h.svc.TicketForGuestToken(ctx, token)
	require.NoError(t, err, "closed: read")
	_, err = h.svc.TicketForGuestWrite(ctx, token)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "closed: no write")
	_, err = h.svc.TicketForGuestWrite(ctx, token+"x")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
	_, err = h.svc.TicketForGuestToken(ctx, token+"x")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "a wrong token reads nothing on a closed ticket either")
}

// The auto-close sweep is the third door into Closed and must agree with the
// other two: the guest's link is left alone.
func TestGuestToken_AutoCloseKeepsTheLinkForReading(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, token := guestTicket(t, h)
	ctx := context.Background()

	_, err := h.svc.Resolve(ctx, tk.ID, "done", staff)
	require.NoError(t, err)
	// The resolution rotated; the guest holds the link that mail carried.
	resolved := lastEventOfType(t, h, notification.EventTicketResolved).GuestToken
	require.NotEmpty(t, resolved)
	require.NotEqual(t, token, resolved)

	// Window 0 days: the next sweep closes it.
	n, err := h.svc.AutoClose(ctx, 0, 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := h.svc.TicketForGuestToken(ctx, resolved)
	require.NoError(t, err, "auto-close must not revoke the link the guest was last sent")
	require.Equal(t, tk.ID, got.ID)
	_, err = h.svc.TicketForGuestWrite(ctx, resolved)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
}

// The re-request flow needs both halves to match.
//
// A closed ticket still matches (#349, decision recorded in DESIGN.md → Guest
// Submission): the guest may ask for a new link to READ their archived ticket.
// This test used to pin the opposite, "a re-request must not undo revocation",
// which stopped being a rule when closing stopped revoking.
func TestGuestToken_ReRequestNeedsBothHalvesAndStillFindsAClosedTicket(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)
	ctx := context.Background()

	id, err := h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "GUEST@EXAMPLE.TEST")
	require.NoError(t, err, "the address match is case-insensitive")
	require.Equal(t, tk.ID, id)

	_, err = h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "someone@else.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "the address alone must not be guessable past")

	_, err = h.svc.GuestTicketIDFor(ctx, "GHD-2026-999999", "guest@example.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)

	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))
	id, err = h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "guest@example.test")
	require.NoError(t, err, "a closed ticket is archived, not erased: its guest can ask for a read-only link")
	require.Equal(t, tk.ID, id)
	_, err = h.svc.GuestTicketIDFor(ctx, tk.TrackingNumber, "someone@else.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "both halves still have to match")
}

func lastEventOfType(t *testing.T, h *harness, ty notification.EventType) notification.Event {
	t.Helper()
	for i := len(h.dispatcher.events) - 1; i >= 0; i-- {
		if h.dispatcher.events[i].Type == ty {
			return h.dispatcher.events[i]
		}
	}
	t.Fatalf("no %s event was dispatched; got %v", ty, h.dispatcher.types())
	return notification.Event{}
}

// A guest reply carries no author, which is what lets a reader tell the
// customer's words from staff's without another column.
func TestGuestReply_HasNoAuthorAndIsNeverInternal(t *testing.T) {
	h := newHarness(t)
	tk, _ := guestTicket(t, h)

	r, err := h.svc.AddGuestReply(context.Background(), tk.ID, "still broken", 7, h.newStatus.ID)
	require.NoError(t, err)
	require.Nil(t, r.AuthorID, "a guest has no account to be the author")
	require.False(t, r.Internal, "a guest cannot write a staff-only note")
	require.False(t, r.NotifyCustomer, "mailing the customer their own message back is noise")
}

// The lifecycle rules apply to a guest exactly as they apply to the reporter:
// the window reopens a resolved ticket, and a closed one refuses.
func TestGuestReply_ObeysTheLifecycleItDidNotAuthor(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("reopens a resolved ticket inside the window", func(t *testing.T) {
		h := newHarness(t)
		tk, _ := guestTicket(t, h)
		_, err := h.svc.Resolve(context.Background(), tk.ID, "done", staff)
		require.NoError(t, err)

		_, err = h.svc.AddGuestReply(context.Background(), tk.ID, "not fixed", 7, h.newStatus.ID)
		require.NoError(t, err)

		after, err := h.store.GetByID(context.Background(), tk.ID)
		require.NoError(t, err)
		require.Equal(t, h.newStatus.ID, after.StatusID, "the link is how a guest reopens")
		require.Nil(t, after.ResolvedAt)
	})

	t.Run("is refused outside the window", func(t *testing.T) {
		h := newHarness(t)
		tk, _ := guestTicket(t, h)
		_, err := h.svc.Resolve(context.Background(), tk.ID, "done", staff)
		require.NoError(t, err)

		_, err = h.svc.AddGuestReply(context.Background(), tk.ID, "too late", 0, h.newStatus.ID)
		require.ErrorIs(t, err, ticket.ErrReopenWindowClosed,
			"the window does not widen because the reply arrived by link")
	})

	t.Run("is refused on a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		tk, _ := guestTicket(t, h)
		require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))

		_, err := h.svc.AddGuestReply(context.Background(), tk.ID, "hello", 7, h.newStatus.ID)
		require.ErrorIs(t, err, ticket.ErrClosed)
	})
}

// Replying is the one thing a guest comes back to do, and it used to lock them
// out: a reply rotated the token unconditionally, while a guest's own reply
// carries no recipient, so the replacement was minted and never sent.
//
// The rule is now rotate-iff-deliver, so these three cases mint nothing.
func TestGuestToken_SurvivesEveryReplyThatMailsNothing(t *testing.T) {
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("the guest's own reply", func(t *testing.T) {
		h := newHarness(t)
		tk, token := guestTicket(t, h)

		_, err := h.svc.AddGuestReply(context.Background(), tk.ID, "still broken", 7, h.newStatus.ID)
		require.NoError(t, err)

		_, err = h.svc.TicketForGuestToken(context.Background(), token)
		require.NoError(t, err, "a guest must not lose their link by using it")
		require.Equal(t, 1, h.store.guestTokenCount(tk.ID))
	})

	t.Run("a staff reply with notify_customer off", func(t *testing.T) {
		h := newHarness(t)
		tk, token := guestTicket(t, h)

		_, err := h.svc.AddReply(context.Background(), tk.ID, "noting this",
			false, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		_, err = h.svc.TicketForGuestToken(context.Background(), token)
		require.NoError(t, err, "nothing was mailed, so nothing may be replaced")
	})

	t.Run("an internal note", func(t *testing.T) {
		h := newHarness(t)
		tk, token := guestTicket(t, h)

		_, err := h.svc.AddReply(context.Background(), tk.ID, "cost code 4471",
			true, false, "", staff, 7, h.newStatus.ID)
		require.NoError(t, err)

		_, err = h.svc.TicketForGuestToken(context.Background(), token)
		require.NoError(t, err)
	})
}

// The invariant, stated directly: a rotation always has somewhere to go.
func TestGuestToken_IsNeverMintedWithoutARecipient(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)
	ctx := context.Background()

	_, err := h.svc.AddGuestReply(ctx, tk.ID, "one", 7, h.newStatus.ID)
	require.NoError(t, err)
	_, err = h.svc.AddReply(ctx, tk.ID, "two", false, false, "", staff, 7, h.newStatus.ID)
	require.NoError(t, err)
	_, err = h.svc.AddReply(ctx, tk.ID, "three", false, true, "guest@example.test", staff, 7, h.newStatus.ID)
	require.NoError(t, err)

	for _, ev := range h.dispatcher.events {
		if ev.GuestToken != "" {
			require.NotEmpty(t, ev.Recipient,
				"%s minted a token with nobody to send it to", ev.Type)
		}
	}
}

// Moving to Closed by status change neither rotates nor revokes (#349).
//
// It used to revoke, and this test pinned "leaves no token behind". Now the
// token the guest holds is left exactly as it is, and — as before — nothing is
// mailed: rotating would send a link to a ticket nobody can act on.
func TestGuestToken_StatusChangeToClosedChangesNothing(t *testing.T) {
	h := newHarness(t)
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	tk, first := guestTicket(t, h)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.closedStatus.ID, admin)
	require.NoError(t, err)

	ev := lastEventOfType(t, h, notification.EventTicketStatusChanged)
	require.Empty(t, ev.GuestToken,
		"closing must not mail a link to a ticket that accepts nothing")
	require.Equal(t, 1, h.store.guestTokenCount(tk.ID),
		"and must neither remove nor add a token")
	_, err = h.svc.TicketForGuestToken(context.Background(), first)
	require.NoError(t, err, "the guest's link keeps reading the archive")
}

// Closing by status change sends no mail of its own, as Close() does not:
// there is no new link to send, and the account URL it used to fall back to
// is a page a guest has no account to open.
func TestGuestToken_ClosingByStatusTellsNobody(t *testing.T) {
	h := newHarness(t)
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}
	tk, _ := guestTicket(t, h)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.closedStatus.ID, admin)
	require.NoError(t, err)

	ev := lastEventOfType(t, h, notification.EventTicketStatusChanged)
	require.Empty(t, ev.GuestToken)
	require.Empty(t, ev.Recipient,
		"no new link means nothing to send, so the mail must not go at all")
}

// Every other status change still reaches the guest — the exclusion is closing,
// not status changes.
func TestGuestToken_OtherStatusChangesStillNotify(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, _ := guestTicket(t, h)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.resolvedStatus.ID, staff)
	require.NoError(t, err)

	ev := lastEventOfType(t, h, notification.EventTicketStatusChanged)
	require.NotEmpty(t, ev.GuestToken)
	require.Equal(t, "guest@example.test", ev.Recipient)
}

// #164: a change marks its event for a guest link and creates no token. The
// token is created when the email is sent (IssueGuestLink), so the event that
// goes into the outbox holds no credential, and a rollback has nothing to
// take back.
func TestGuestLink_AChangeMarksTheEventAndCreatesNoToken(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.sendTime = nil // the queue only, no send
	email := "guest@example.test"
	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer broken", Description: "it is", CategoryID: uuid.New(),
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)
	require.Len(t, h.dispatcher.events, 1)
	ev := h.dispatcher.events[0]
	require.True(t, ev.GuestLink)
	require.Empty(t, ev.GuestToken, "the change created a token")
	require.Empty(t, h.store.guestTokens, "a token row was written at the change")
}

// The send-time half on a ticket that closed after the event (#349).
//
// This replaces the skip #346 documented ("a closed ticket gets no link"),
// which is why a guest could not read the last reply on a ticket closed
// straight after it. A closed ticket now gets its pending mail, and the link
// in it is a NEW token added beside the existing ones — never a rotation,
// because rotating would kill the link the guest already holds, and a closed
// ticket's links are its archive. Raw tokens are stored hashed, so the
// "existing" link cannot be re-sent verbatim; adding is the nearest thing
// that leaves every link already issued working.
func TestGuestLink_IssueOnAClosedTicketAddsAReadLinkAndKeepsTheOthers(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, first := guestTicket(t, h)
	ctx := context.Background()
	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))

	ev, ok, err := h.svc.IssueGuestLink(ctx,
		notification.Event{Type: notification.EventTicketReplied, TicketID: tk.ID, GuestLink: true})
	require.NoError(t, err)
	require.True(t, ok, "a closed ticket still gets its pending mail")
	require.NotEmpty(t, ev.GuestToken)
	require.NotEqual(t, first, ev.GuestToken)
	require.Equal(t, "guest@example.test", ev.Recipient)

	require.Equal(t, 2, h.store.guestTokenCount(tk.ID), "added, not rotated")
	_, err = h.svc.TicketForGuestToken(ctx, first)
	require.NoError(t, err, "the link the guest already holds keeps working")
	_, err = h.svc.TicketForGuestToken(ctx, ev.GuestToken)
	require.NoError(t, err, "the new link reads")
	_, err = h.svc.TicketForGuestWrite(ctx, ev.GuestToken)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound,
		"a link minted for a closed ticket must grant nothing but a read")
}

// The headline case of #349: staff answer and close straight away. The reply's
// mail is sent after the close and must carry a link that works.
func TestGuestLink_ReplyThenCloseDeliversAWorkingLink(t *testing.T) {
	h := newHarness(t)
	h.dispatcher.sendTime = nil // queue only; the send happens after the close
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	email := "guest@example.test"
	ctx := context.Background()
	tk, err := h.svc.Create(ctx, ticket.CreateInput{
		Subject: "Printer broken", Description: "it is", CategoryID: uuid.New(),
		GuestEmail: &email, GuestName: "Ada",
	})
	require.NoError(t, err)

	_, err = h.svc.AddReply(ctx, tk.ID, "fixed, replaced the drum",
		false, true, email, staff, 7, h.newStatus.ID)
	require.NoError(t, err)
	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))

	queued := lastEventOfType(t, h, notification.EventTicketReplied)
	require.True(t, queued.GuestLink)
	sent, ok, err := h.svc.IssueGuestLink(ctx, queued)
	require.NoError(t, err)
	require.True(t, ok, "#346 sent nothing here: the guest never heard about the answer")
	require.NotEmpty(t, sent.GuestToken)

	got, err := h.svc.TicketForGuestToken(ctx, sent.GuestToken)
	require.NoError(t, err, "and the link in the mail must open the thread")
	require.Equal(t, tk.ID, got.ID)
}

// "Existing link" has no meaning when none exists — the ticket never had one,
// or its links all expired. A closed ticket then gets a new READ link, never
// nothing: staff wrote to the guest, and the mail is the only way they learn.
// The new link is bounded like any other (GuestTokenTTL) and cannot write.
func TestGuestLink_ClosedTicketWithNoLiveLinkGetsANewReadOnlyOne(t *testing.T) {
	h := newHarness(t)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	tk, first := guestTicket(t, h)
	ctx := context.Background()
	require.NoError(t, h.svc.Close(ctx, tk.ID, staff))
	h.store.expireGuestTokens(tk.ID)
	_, err := h.svc.TicketForGuestToken(ctx, first)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound, "precondition: the old link has expired")

	ev, ok, err := h.svc.IssueGuestLink(ctx,
		notification.Event{Type: notification.EventTicketReplied, TicketID: tk.ID, GuestLink: true})
	require.NoError(t, err)
	require.True(t, ok)
	_, err = h.svc.TicketForGuestToken(ctx, ev.GuestToken)
	require.NoError(t, err)
	_, err = h.svc.TicketForGuestWrite(ctx, ev.GuestToken)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
	require.WithinDuration(t, time.Now().Add(ticket.GuestTokenTTL), h.store.latestGuestTokenExpiry(tk.ID), time.Minute)
}

// An open ticket still rotates: the new rule is about Closed only.
func TestGuestLink_IssueOnAnOpenTicketStillRotates(t *testing.T) {
	h := newHarness(t)
	tk, first := guestTicket(t, h)
	ctx := context.Background()

	ev, ok, err := h.svc.IssueGuestLink(ctx,
		notification.Event{Type: notification.EventTicketReplied, TicketID: tk.ID, GuestLink: true})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, h.store.guestTokenCount(tk.ID))
	_, err = h.svc.TicketForGuestToken(ctx, first)
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
	_, err = h.svc.TicketForGuestWrite(ctx, ev.GuestToken)
	require.NoError(t, err, "an open ticket's link writes")
}
