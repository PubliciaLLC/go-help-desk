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
	inner   *fakeAtomic
	between func()
}

func (a *rerunAtomic) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
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
	svc := ticket.NewService(h.store, h.statuses, h.dispatcher, h.auditStore,
		&rerunAtomic{inner: h.atomic, between: between}, h.sla)
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

	got, err := svc.Create(context.Background(), ticket.CreateInput{
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
