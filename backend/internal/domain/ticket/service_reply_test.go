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

// harness wires a Service over the in-memory doubles and returns them so a test
// can inject failures and inspect what was written.
type harness struct {
	svc        *ticket.Service
	store      *fakeStore
	statuses   *fakeStatusStore
	dispatcher *fakeDispatcher
	auditStore *fakeAuditStore
	atomic     *fakeAtomic
	sla        *fakeSLA

	newStatus      ticket.Status
	resolvedStatus ticket.Status
	closedStatus   ticket.Status
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	newSt := ticket.Status{ID: uuid.New(), Name: ticket.StatusNameNew, Kind: ticket.StatusKindSystem, Active: true}
	resolvedSt := ticket.Status{ID: uuid.New(), Name: ticket.StatusNameResolved, Kind: ticket.StatusKindSystem, Active: true}
	closedSt := ticket.Status{ID: uuid.New(), Name: ticket.StatusNameClosed, Kind: ticket.StatusKindSystem, Active: true}

	statuses := &fakeStatusStore{
		byName: map[string]ticket.Status{
			ticket.StatusNameNew:      newSt,
			ticket.StatusNameResolved: resolvedSt,
			ticket.StatusNameClosed:   closedSt,
		},
		counts: make(map[uuid.UUID]int64),
	}

	h := &harness{
		store:          newFakeStore(),
		statuses:       statuses,
		dispatcher:     &fakeDispatcher{},
		auditStore:     &fakeAuditStore{},
		sla:            &fakeSLA{},
		newStatus:      newSt,
		resolvedStatus: resolvedSt,
		closedStatus:   closedSt,
	}
	h.atomic = &fakeAtomic{store: h.store, audit: h.auditStore}
	h.svc = ticket.NewService(h.store, statuses, h.dispatcher, h.auditStore, h.atomic, h.sla)
	require.NoError(t, h.svc.LoadSystemStatuses(context.Background()))
	return h
}

// seedResolved plants a ticket sitting in Resolved, recently enough that a user
// reply falls inside the reopen window.
func (h *harness) seedResolved(reporterID uuid.UUID) ticket.Ticket {
	resolvedAt := time.Now().Add(-24 * time.Hour)
	t := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		ReporterUserID: &reporterID,
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(t)
	return t
}

// TestAddReply_ReopenPersistFailureIsNotAnnounced pins the most damaging half
// of the discarded-error problem.
//
// On the auto-reopen path the service assigns the new status, calls
// store.Update, and then — regardless of whether that write succeeded —
// records a status-history entry and dispatches a TicketReopened notification.
// The write result is discarded with `_ =`.
//
// So when the update fails, the ticket stays Resolved while the system tells
// everyone it was reopened: a history entry for a transition that did not
// happen, and an email to the reporter about it. The stored state and the
// story told about the stored state disagree, permanently and silently.
//
// Announcing a state change must be conditional on having persisted it.
func TestAddReply_ReopenPersistFailureIsNotAnnounced(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	seeded := h.seedResolved(reporter)

	h.store.errUpdate = errStoreDown

	_, err := h.svc.AddReply(context.Background(), seeded.ID,
		"It is broken again", false, true, "reporter@example.com",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser},
		30, h.newStatus.ID)

	require.Error(t, err, "a failed status write must surface, not be discarded")
	require.ErrorIs(t, err, errStoreDown)

	require.NotContains(t, h.dispatcher.types(), notification.EventTicketReopened,
		"no reopen notification may be sent when the reopen was not persisted")

	require.Equal(t, 0, h.store.historyCreates,
		"no status-history entry may be written for a transition that failed")

	stored, getErr := h.store.GetByID(context.Background(), seeded.ID)
	require.NoError(t, getErr)
	require.Equal(t, h.resolvedStatus.ID, stored.StatusID,
		"the ticket must remain in its stored status")
}

// TestAddReply_SLAFailureDoesNotLoseTheReply is the other side of the same
// coin. RecordFirstResponse is genuinely ancillary — a metric, not the user's
// intent — so an SLA outage must not fail a reply that is already persisted.
//
// It pins the boundary the fix must respect: the previous test demands that a
// failed write stop the operation, and this one demands that this particular
// failure does not. Getting that distinction wrong in either direction is a bug.
func TestAddReply_SLAFailureDoesNotLoseTheReply(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	agent := uuid.New()

	seeded := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000002",
		Subject:        "Mouse not working",
		ReporterUserID: &reporter,
		StatusID:       h.newStatus.ID,
		CreatedAt:      time.Now().Add(-time.Hour),
		UpdatedAt:      time.Now().Add(-time.Hour),
	}
	h.store.seed(seeded)

	h.sla.err = errStoreDown

	reply, err := h.svc.AddReply(context.Background(), seeded.ID,
		"Have you tried a new battery?", false, true, "reporter@example.com",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff},
		30, h.newStatus.ID)

	require.NoError(t, err, "an SLA metric failure must not fail the reply")
	require.NotEmpty(t, reply.ID)
	require.Equal(t, 1, h.store.replyCreates, "the reply must still be persisted")
}

// TestAddReply_SucceedsAndReopens is the positive control: with every double
// healthy, the reopen path persists the status, writes exactly one history
// entry, and dispatches the reopen event. Without this, the test above could
// pass for the wrong reason — by the reopen never running at all.
func TestAddReply_SucceedsAndReopens(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	seeded := h.seedResolved(reporter)

	reply, err := h.svc.AddReply(context.Background(), seeded.ID,
		"Still broken", false, true, "reporter@example.com",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser},
		30, h.newStatus.ID)

	require.NoError(t, err)
	require.NotEmpty(t, reply.ID)

	stored, err := h.store.GetByID(context.Background(), seeded.ID)
	require.NoError(t, err)
	require.Equal(t, h.newStatus.ID, stored.StatusID, "the ticket must be reopened")
	require.Nil(t, stored.ResolvedAt, "reopening must clear the resolved timestamp")

	require.Equal(t, 1, h.store.historyCreates, "exactly one history entry")
	require.Contains(t, h.dispatcher.types(), notification.EventTicketReopened)
}

// TestAddReply_InternalNoteNeverNotifiesCustomer pins an existing rule that the
// reply path enforces and nothing covered.
func TestAddReply_InternalNoteNeverNotifiesCustomer(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	agent := uuid.New()

	seeded := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000003",
		Subject:        "Cannot print",
		ReporterUserID: &reporter,
		StatusID:       h.newStatus.ID,
		CreatedAt:      time.Now().Add(-time.Hour),
		UpdatedAt:      time.Now().Add(-time.Hour),
	}
	h.store.seed(seeded)

	// notifyCustomer is requested, but the note is internal.
	reply, err := h.svc.AddReply(context.Background(), seeded.ID,
		"Reporter is mistaken, see ticket 12", true, true, "reporter@example.com",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff},
		30, h.newStatus.ID)

	require.NoError(t, err)
	require.True(t, reply.Internal)
	require.False(t, reply.NotifyCustomer,
		"an internal note must never be flagged for customer notification")
}

// statusNamed returns a seeded status by name, failing loudly on a typo rather
// than silently returning a zero Status.
func (h *harness) statusNamed(name string) ticket.Status {
	st, ok := h.statuses.byName[name]
	if !ok {
		panic("no seeded status named " + name)
	}
	return st
}

// seedOpen plants a ticket in New.
func (h *harness) seedOpen() ticket.Ticket {
	reporter := uuid.New()
	t := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000100",
		Subject:        "Open ticket",
		ReporterUserID: &reporter,
		StatusID:       h.newStatus.ID,
		CreatedAt:      time.Now().Add(-2 * time.Hour),
		UpdatedAt:      time.Now().Add(-2 * time.Hour),
	}
	h.store.seed(t)
	return t
}

// seedClosed plants a ticket in Closed, with the timestamps a real close leaves.
func (h *harness) seedClosed() ticket.Ticket {
	reporter := uuid.New()
	closedAt := time.Now().Add(-time.Hour)
	t := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000101",
		Subject:        "Closed ticket",
		ReporterUserID: &reporter,
		StatusID:       h.closedStatus.ID,
		ResolvedAt:     &closedAt,
		ClosedAt:       &closedAt,
		CreatedAt:      time.Now().Add(-4 * time.Hour),
		UpdatedAt:      closedAt,
	}
	h.store.seed(t)
	return t
}
