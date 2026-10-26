package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The transaction runner re-runs a closure after Postgres kills the first
// attempt (#508). A closure that keeps something from the attempt that was
// rolled back acts on state that no longer exists.
//
// rerunAtomic forces exactly that: the first attempt runs to the end and is
// then failed, as a deadlock or a failed commit would; the database is changed
// underneath, as a concurrent request would; and the closure runs again.

var errKilled = errors.New("killed to break a lock cycle")

type rerunAtomic struct {
	inner *fakeAtomic
	// before runs once the service has read the ticket and is about to open the
	// transaction; between runs after the first attempt is rolled back.
	before  func()
	between func()
}

func (a *rerunAtomic) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	if a.before != nil {
		a.before()
	}
	err := a.inner.InTx(ctx, func(st ticket.Store, au audit.Store, ch *notification.Changes) error {
		if err := fn(st, au, ch); err != nil {
			return err
		}
		return errKilled
	})
	if !errors.Is(err, errKilled) {
		return err
	}
	a.between()
	return a.inner.InTx(ctx, fn)
}

// rerunService is the harness's service, run through rerunAtomic.
func (h *harness) rerunService(t *testing.T, between func()) *ticket.Service {
	t.Helper()
	return h.rerunServiceWith(t, nil, between)
}

func (h *harness) rerunServiceWith(t *testing.T, before, between func()) *ticket.Service {
	t.Helper()
	svc := ticket.NewService(h.store, h.statuses, h.dispatcher, h.auditStore,
		&rerunAtomic{inner: h.atomic, before: before, between: between}, h.sla)
	require.NoError(t, svc.LoadSystemStatuses(context.Background()))
	return svc
}

// The first attempt routed to a colleague who was then disabled. The retry
// finds nobody to route to, and must create the ticket unassigned rather than
// give it to somebody it just found unassignable.
func TestCreate_RetryDoesNotKeepTheRoutingOfTheRolledBackAttempt(t *testing.T) {
	h := newHarness(t)
	reporter, colleague := uuid.New(), uuid.New()
	svc := h.rerunService(t, func() { h.store.notPeople = map[uuid.UUID]bool{colleague: true} })

	got, err := svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject:        "Routed",
		ReporterUserID: &reporter,
		AutoAssign:     []ticket.Assignment{{UserID: &colleague}},
	})
	require.NoError(t, err)

	require.Nil(t, got.AssigneeUserID, "assigned to an account the retry found unassignable")
	stored, err := h.store.GetByID(context.Background(), got.ID)
	require.NoError(t, err)
	require.Nil(t, stored.AssigneeUserID)
}

// The first attempt found the duplicate link. It was removed before the retry,
// which must create it rather than resolve the ticket on a link that is gone.
func TestResolveAsDuplicate_RetryDoesNotKeepTheLinkFoundByTheRolledBackAttempt(t *testing.T) {
	h := newHarness(t)
	src, target := h.seedOpen(), h.seedOpen()
	h.store.links[src.ID] = []ticket.TicketLink{
		{SourceTicketID: src.ID, TargetTicketID: target.ID, LinkType: ticket.LinkDuplicateOf},
	}
	svc := h.rerunService(t, func() { delete(h.store.links, src.ID) })

	agent := uuid.New()
	_, err := svc.ResolveAsDuplicate(context.Background(), src.ID, target.ID, "dup",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Len(t, h.store.links[src.ID], 1, "resolved as a duplicate with no duplicate link")
	require.Equal(t, ticket.LinkDuplicateOf, h.store.links[src.ID][0].LinkType)
}

// The first attempt saw an already-Closed ticket; it was reopened before the
// retry, which then closes it and has to say so (the SLA is told, once).
func TestClose_RetryDoesNotKeepTheAlreadyClosedOfTheRolledBackAttempt(t *testing.T) {
	h := newHarness(t)
	tk := h.seedClosed()
	svc := h.rerunService(t, func() {
		reopened := h.store.tickets[tk.ID]
		reopened.StatusID = h.resolvedStatus.ID
		h.store.tickets[tk.ID] = reopened
	})

	agent := uuid.New()
	require.NoError(t, svc.Close(context.Background(), tk.ID, ticket.Actor{UserID: &agent, Role: user.RoleStaff}))

	require.Equal(t, h.closedStatus.ID, h.store.tickets[tk.ID].StatusID)
	require.Equal(t, 1, h.sla.resolutions, "the retry closed the ticket and reported that it did not")
}

// A requester replies to a Resolved ticket. Staff move it to In Progress after
// the service read it, so attempt 1 sees the reopen's condition gone and
// leaves the status alone; the attempt is then killed, and the ticket is
// Resolved again for the retry. The retry must reopen it, with the status, the
// history and the reopen count all agreeing.
func TestAddReply_RetryDoesNotKeepTheLockedRowOfTheRolledBackAttempt(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	tk := h.seedResolved(reporter)
	setStatus := func(id uuid.UUID) {
		moved := h.store.tickets[tk.ID]
		moved.StatusID = id
		h.store.tickets[tk.ID] = moved
	}
	svc := h.rerunServiceWith(t,
		func() { setStatus(h.inProgressStatus.ID) },
		func() { setStatus(h.resolvedStatus.ID) })

	_, err := svc.AddReply(context.Background(), tk.ID, "Still broken", ticket.TextFormatPlain, false, true, "reporter@example.com",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser}, 30, h.newStatus.ID)
	require.NoError(t, err)

	stored := h.store.tickets[tk.ID]
	require.Equal(t, h.newStatus.ID, stored.StatusID, "the reply reopened nothing: the ticket is still Resolved")
	require.Equal(t, 1, stored.ReopenCount)
	require.Len(t, h.store.history, 1)
	require.Equal(t, h.resolvedStatus.ID, *h.store.history[0].FromStatusID)
	require.Equal(t, stored.StatusID, h.store.history[0].ToStatusID, "the history does not match the status")
}

// The first attempt issued a link; the guest's address was removed before the
// retry, which finds no recipient and has nothing to return. It used to return
// the first attempt's "ok" and dereference the address that was gone.
func TestIssueGuestLink_RetryDoesNotKeepTheTokenOfTheRolledBackAttempt(t *testing.T) {
	h := newHarness(t)
	tk, _ := guestTicket(t, h)
	svc := h.rerunService(t, func() {
		gone := h.store.tickets[tk.ID]
		gone.GuestEmail = nil
		h.store.tickets[tk.ID] = gone
	})

	ev, ok, err := svc.IssueGuestLink(context.Background(),
		notification.Event{Type: notification.EventTicketReplied, TicketID: tk.ID, GuestLink: true})
	require.NoError(t, err)
	require.False(t, ok, "a ticket with no guest address has no link to send")
	require.Empty(t, ev.GuestToken)
}
