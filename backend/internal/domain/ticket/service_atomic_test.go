package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Every composite ticket operation writes to several tables — the ticket row,
// its status history, its audit entry. Before #75 those were independent
// statements, and the history and audit writes discarded their errors
// outright, so a failure part-way through left the database half-applied and
// left no trace of having done so.
//
// These tests fail a write that used to be ignored and assert that the whole
// operation is undone. The fake runner really does restore its snapshot, so
// they would pass trivially if the service had no transaction at all — the
// audit-failure cases below are the ones that distinguish the two, because
// they fail a write the old code never even checked.

func TestCreate_AuditFailureRollsBackTheTicket(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	h.auditStore.err = errStoreDown

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:        "Audit is down",
		ReporterUserID: &reporter,
	})

	require.Error(t, err, "a failed audit write must fail the operation")
	require.ErrorIs(t, err, errStoreDown)

	// The point of the change. Previously the audit write was `_ =` and the
	// ticket committed regardless, so the trail silently lost an entry.
	require.Equal(t, 0, h.store.creates, "no ticket may survive a failed audit write")
	require.Empty(t, h.store.tickets, "the store must be back to its prior state")
	require.Equal(t, 1, h.atomic.rollbacks)
	require.Equal(t, 0, h.atomic.commits)
	require.Empty(t, h.dispatcher.events, "nothing may be announced for a rolled-back ticket")
}

func TestCreate_HistoryFailureRollsBackTheTicket(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	h.store.errCreateHistory = errStoreDown

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:        "History is down",
		ReporterUserID: &reporter,
	})

	require.Error(t, err)
	require.Equal(t, 0, h.store.creates, "a ticket without its opening status is not a ticket")
	require.Empty(t, h.auditStore.entries)
	require.Equal(t, 1, h.atomic.rollbacks)
}

func TestUpdateStatus_AuditFailureLeavesTheStatusAlone(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	actorID := uuid.New()
	h.auditStore.err = errStoreDown

	_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
		ticket.Actor{UserID: &actorID, Role: user.RoleStaff})

	require.Error(t, err)

	stored, getErr := h.store.GetByID(context.Background(), seeded.ID)
	require.NoError(t, getErr)
	require.Equal(t, h.newStatus.ID, stored.StatusID,
		"an unauditable status change must not persist")
	require.Equal(t, 1, h.atomic.rollbacks)
	require.Empty(t, h.dispatcher.events)
}

func TestAddReply_ReopenFailureRollsBackTheReply(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	seeded := h.seedResolved(reporter)
	h.store.errUpdate = errStoreDown

	_, err := h.svc.AddReply(context.Background(), seeded.ID,
		"It is broken again", false, true, "reporter@example.com",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser},
		30, h.newStatus.ID)

	require.Error(t, err)

	// The reply and the reopen are one unit. Previously the reply committed and
	// the reopen did not, leaving a user reply sitting under a Resolved ticket.
	require.Empty(t, h.store.replies[seeded.ID], "the reply must not survive a failed reopen")
	stored, getErr := h.store.GetByID(context.Background(), seeded.ID)
	require.NoError(t, getErr)
	require.Equal(t, h.resolvedStatus.ID, stored.StatusID)
	require.Equal(t, 1, h.atomic.rollbacks)
}

func TestResolve_AuditFailureLeavesTheTicketOpen(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()
	h.auditStore.err = errStoreDown

	_, err := h.svc.Resolve(context.Background(), seeded.ID, "Fixed it",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, h.newStatus.ID, stored.StatusID)
	require.Nil(t, stored.ResolvedAt, "the resolution timestamp must not persist either")
	require.Equal(t, 1, h.atomic.rollbacks)
}

// TestOperations_CommitOnce is the positive control. Without it the tests above
// could pass because the operations never ran, and it also pins that each does
// exactly one transaction rather than several.
func TestOperations_CommitOnce(t *testing.T) {
	reporter := uuid.New()
	agent := uuid.New()

	cases := []struct {
		name string
		run  func(*harness) error
	}{
		{
			name: "Create",
			run: func(h *harness) error {
				_, err := h.svc.Create(context.Background(), ticket.CreateInput{
					Subject: "Fine", ReporterUserID: &reporter,
				})
				return err
			},
		},
		{
			name: "UpdateStatus",
			run: func(h *harness) error {
				seeded := h.seedOpen()
				_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
					ticket.Actor{UserID: &agent, Role: user.RoleStaff})
				return err
			},
		},
		{
			name: "Assign",
			run: func(h *harness) error {
				seeded := h.seedOpen()
				_, err := h.svc.Assign(context.Background(), seeded.ID, &agent, nil,
					ticket.Actor{UserID: &agent, Role: user.RoleStaff})
				return err
			},
		},
		{
			name: "Resolve",
			run: func(h *harness) error {
				seeded := h.seedOpen()
				_, err := h.svc.Resolve(context.Background(), seeded.ID, "done",
					ticket.Actor{UserID: &agent, Role: user.RoleStaff})
				return err
			},
		},
		{
			name: "Close",
			run: func(h *harness) error {
				seeded := h.seedOpen()
				return h.svc.Close(context.Background(), seeded.ID)
			},
		},
		{
			name: "Reopen",
			run: func(h *harness) error {
				seeded := h.seedClosed()
				_, err := h.svc.Reopen(context.Background(), seeded.ID, h.newStatus.ID,
					ticket.Actor{UserID: &agent, Role: user.RoleStaff})
				return err
			},
		},
		{
			name: "AddReply",
			run: func(h *harness) error {
				seeded := h.seedOpen()
				_, err := h.svc.AddReply(context.Background(), seeded.ID, "hello",
					false, true, "r@example.com",
					ticket.Actor{UserID: &agent, Role: user.RoleStaff}, 30, h.newStatus.ID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			require.NoError(t, tc.run(h))
			require.Equal(t, 1, h.atomic.commits, "%s must use exactly one transaction", tc.name)
			require.Equal(t, 0, h.atomic.rollbacks)
		})
	}
}

// TestCreate_TransactionUnavailable covers the runner itself failing, which is
// what a saturated connection pool looks like.
func TestCreate_TransactionUnavailable(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	h.atomic.errBeginTx = errStoreDown

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject: "No connections left", ReporterUserID: &reporter,
	})

	require.ErrorIs(t, err, errStoreDown)
	require.Equal(t, 0, h.store.creates)
	require.Empty(t, h.dispatcher.events)
}
