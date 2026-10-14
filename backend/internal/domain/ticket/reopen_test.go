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

// Reopen (#464): a ticket that moves from Resolved or Closed back to an open
// status is flagged (reopen_count), its priority goes up one level on the FIRST
// reopen only, its SLA starts over, and the reopen is announced with who holds
// the ticket so staff can be mailed. All of it commits with the status change
// or none of it does.

// reopenDoor performs one way of reopening the ticket and says who did it.
type reopenDoor struct {
	name string
	from string // "resolved" or "closed"
	do   func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error)
}

var (
	reopenStaffID  = uuid.New()
	reopenStaff    = ticket.Actor{UserID: &reopenStaffID, Role: user.RoleStaff}
	reopenAdminID  = uuid.New()
	reopenAdmin    = ticket.Actor{UserID: &reopenAdminID, Role: user.RoleAdmin}
	reopenAssignee = uuid.New()
	reopenGroup    = uuid.New()
)

// seedFinished plants a ticket in Resolved or Closed at the given priority,
// held by reopenAssignee and reopenGroup, with an SLA record that met its
// targets.
func (h *prioHarness) seedFinished(from string, p ticket.Priority) ticket.Ticket {
	reporter := uuid.New()
	at := time.Now().Add(-24 * time.Hour)
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: "HD-000900", Subject: "Printer offline",
		ReporterUserID: &reporter, Priority: p,
		AssigneeUserID: ptr(reopenAssignee), AssigneeGroupID: ptr(reopenGroup),
		CreatedAt: at.Add(-24 * time.Hour), UpdatedAt: at,
	}
	switch from {
	case "resolved":
		tk.StatusID, tk.ResolvedAt = h.resolvedStatus.ID, &at
	case "closed":
		tk.StatusID, tk.ResolvedAt, tk.ClosedAt = h.closedStatus.ID, &at, &at
	}
	h.store.seed(tk)
	started := at.Add(-24 * time.Hour)
	policy := h.medium
	if p == ticket.PriorityHigh {
		policy = h.high
	}
	h.store.seedSLARecord(tk.ID, policy, &started, 0, metFacts())
	return tk
}

// reopenDoors is every way the service can move a ticket out of Resolved or
// Closed. The MCP tool update_ticket_status and the REST PATCH are both
// UpdateStatus.
func reopenDoors() []reopenDoor {
	return []reopenDoor{
		{"a requester's reply", "resolved", func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error) {
			a := ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}
			_, err := h.svc.AddReply(context.Background(), tk.ID, "still broken", false, true, "r@example.test", a, 30, h.newStatus.ID)
			return a, err
		}},
		{"a guest's reply", "resolved", func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error) {
			// The ticket has to be a guest's. Seeded over the top of the
			// reporter's, keeping everything else.
			tk.ReporterUserID, tk.GuestEmail = nil, ptrTo("guest@example.test")
			h.store.seed(tk)
			_, err := h.svc.AddGuestReply(context.Background(), tk.ID, "still broken", 30, h.newStatus.ID)
			return ticket.Actor{Role: user.RoleUser}, err
		}},
		{"staff changing the status of a Resolved ticket", "resolved", func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error) {
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
			return reopenStaff, err
		}},
		{"an admin changing the status of a Closed ticket (policy allows)", "closed", func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error) {
			h.policy = ticket.ReopenPolicyStaffAdmin
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.newStatus.ID, reopenAdmin)
			return reopenAdmin, err
		}},
		{"a force-reopen of a Closed ticket", "closed", func(h *prioHarness, tk ticket.Ticket) (ticket.Actor, error) {
			h.policy = ticket.ReopenPolicyStaffAdmin
			_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, reopenStaff)
			return reopenStaff, err
		}},
	}
}

func ptrTo[T any](v T) *T { return &v }

func actionsOf(h *prioHarness, action string) int {
	n := 0
	for _, e := range h.auditStore.entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

// Requirement 1 and 2, on every door: the ticket is flagged, the priority goes
// up one level, and the SLA starts over on a new record.
func TestReopen_EveryDoorFlagsBumpsAndRestartsTheSLA(t *testing.T) {
	for _, d := range reopenDoors() {
		t.Run(d.name, func(t *testing.T) {
			h := newPrioHarness(t)
			tk := h.seedFinished(d.from, ticket.PriorityMedium)

			actor, err := d.do(h, tk)
			require.NoError(t, err)

			got, _ := h.store.GetByID(context.Background(), tk.ID)
			require.Equal(t, 1, got.ReopenCount)
			require.Equal(t, ticket.PriorityHigh, got.Priority, "one level up")
			require.NotEqual(t, h.resolvedStatus.ID, got.StatusID)
			require.NotEqual(t, h.closedStatus.ID, got.StatusID)

			require.Equal(t, 1, actionsOf(h, "reopened"))
			pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
			require.Equal(t, ticket.PriorityChangeReopened, pc.After["priority_change_reason"])
			require.Equal(t, ticket.PriorityMedium, pc.Before["priority"])
			require.Equal(t, ticket.PriorityHigh, pc.After["priority"])
			require.Equal(t, actor.UserID, pc.ActorID)
			onlyEntry(t, h.auditStore.entries, "sla_restarted")

			rows := h.store.slaRows(tk.ID)
			require.Len(t, rows, 2)
			require.NotNil(t, rows[0].supersededAt, "the old record is kept, superseded")
			require.Nil(t, rows[1].supersededAt)
			require.Equal(t, h.high, rows[1].policyID, "re-matched to the new priority")
			require.Equal(t, ticket.SLAFacts{}, rows[1].facts, "both targets timed anew")
		})
	}
}

// Requirement 2: "first reopen only". The count is what remembers it, so a
// ticket that is resolved and reopened again is flagged again, its SLA starts
// over again, and its priority stays where it is.
func TestReopen_OnlyTheFirstReopenBumpsThePriority(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityLow)
	h.store.slaPolicies[ticket.PriorityLow] = uuid.New()
	reporter := tk.ReporterUserID
	requester := ticket.Actor{UserID: reporter, Role: user.RoleUser}
	reply := func() {
		_, err := h.svc.AddReply(context.Background(), tk.ID, "again", false, true, "r@example.test", requester, 30, h.newStatus.ID)
		require.NoError(t, err)
	}

	reply()
	got, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, 1, got.ReopenCount)
	require.Equal(t, ticket.PriorityMedium, got.Priority)

	_, err := h.svc.Resolve(context.Background(), tk.ID, "fixed", reopenStaff)
	require.NoError(t, err)
	reply()
	got, _ = h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, 2, got.ReopenCount, "flagged again")
	require.Equal(t, ticket.PriorityMedium, got.Priority, "but not bumped again")
	require.Equal(t, 1, actionsOf(h, "priority_changed"))
	require.Equal(t, 2, actionsOf(h, "reopened"))
	require.Equal(t, 2, actionsOf(h, "sla_restarted"), "the SLA still starts over on every reopen")

	_, err = h.svc.Resolve(context.Background(), tk.ID, "fixed again", reopenStaff)
	require.NoError(t, err)
	reply()
	got, _ = h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, 3, got.ReopenCount)
	require.Equal(t, ticket.PriorityMedium, got.Priority)
}

// A ticket reopened for the first time at the highest priority stays there; it
// is still flagged and its SLA still starts over.
func TestReopen_CriticalStaysCritical(t *testing.T) {
	h := newPrioHarness(t)
	h.store.slaPolicies[ticket.PriorityCritical] = uuid.New()
	tk := h.seedFinished("resolved", ticket.PriorityCritical)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)

	got, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, ticket.PriorityCritical, got.Priority)
	require.Equal(t, 1, got.ReopenCount)
	require.Zero(t, actionsOf(h, "priority_changed"), "nothing changed, so nothing to record")
	require.Equal(t, 1, actionsOf(h, "sla_restarted"))
}

func TestReopen_SLAOffStillFlagsAndBumps(t *testing.T) {
	h := newPrioHarness(t)
	h.svc.SetSLAEnabled(func(context.Context) bool { return false })
	tk := h.seedFinished("resolved", ticket.PriorityMedium)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)

	got, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, 1, got.ReopenCount)
	require.Equal(t, ticket.PriorityHigh, got.Priority)
	require.Len(t, h.store.slaRows(tk.ID), 1, "no record created or superseded with the SLA off")
}

// Moves that are not a reopen: nothing is flagged, bumped or announced.
func TestReopen_OtherStatusMovesAreNotReopens(t *testing.T) {
	cases := []struct {
		name string
		seed func(h *prioHarness) ticket.Ticket
		to   func(h *prioHarness) uuid.UUID
	}{
		{"open to open", func(h *prioHarness) ticket.Ticket { return h.seedMedium() }, func(h *prioHarness) uuid.UUID { return h.inProgressStatus.ID }},
		{"open to Resolved", func(h *prioHarness) ticket.Ticket { return h.seedMedium() }, func(h *prioHarness) uuid.UUID { return h.resolvedStatus.ID }},
		{"Resolved to Closed", func(h *prioHarness) ticket.Ticket { return h.seedFinished("resolved", ticket.PriorityMedium) }, func(h *prioHarness) uuid.UUID { return h.closedStatus.ID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPrioHarness(t)
			tk := tc.seed(h)

			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, tc.to(h), reopenAdmin)
			require.NoError(t, err)

			got, _ := h.store.GetByID(context.Background(), tk.ID)
			require.Zero(t, got.ReopenCount)
			require.Equal(t, ticket.PriorityMedium, got.Priority)
			require.Zero(t, actionsOf(h, "reopened"))
			require.Zero(t, actionsOf(h, "priority_changed"))
			require.NotContains(t, h.dispatcher.types(), notification.EventTicketReopened)
		})
	}
}

// Requirement 4: the reopen commits with every audit entry it writes, or none
// of it does. The entries are written in order (reopened, priority_changed,
// sla_restarted), so failing the Nth write exercises each.
func TestReopen_AnAuditFailureRollsEverythingBack(t *testing.T) {
	for _, d := range reopenDoors() {
		for call := 1; call <= 3; call++ {
			t.Run(d.name+"/audit write "+string(rune('0'+call)), func(t *testing.T) {
				h := newPrioHarness(t)
				tk := h.seedFinished(d.from, ticket.PriorityMedium)
				rowsBefore := h.store.slaRows(tk.ID)
				h.auditStore.failOnCall = call

				_, err := d.do(h, tk)
				require.Error(t, err)

				got, _ := h.store.GetByID(context.Background(), tk.ID)
				require.Zero(t, got.ReopenCount, "the flag is rolled back")
				require.Equal(t, ticket.PriorityMedium, got.Priority, "the priority is rolled back")
				require.Equal(t, tk.StatusID, got.StatusID, "the status is rolled back")
				require.Equal(t, rowsBefore, h.store.slaRows(tk.ID), "the SLA is rolled back")
				require.Empty(t, h.auditStore.entries)
				require.NotContains(t, h.dispatcher.types(), notification.EventTicketReopened, "nothing is announced")
			})
		}
	}
}

// Erik's standing rule: a refused request changes nothing.
func TestReopen_ARefusedReopenChangesNothing(t *testing.T) {
	cases := map[string]func(h *prioHarness, tk ticket.Ticket) error{
		"Closed, status change, policy off": func(h *prioHarness, tk ticket.Ticket) error {
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.newStatus.ID, reopenAdmin)
			return err
		},
		"Closed, force-reopen, policy off": func(h *prioHarness, tk ticket.Ticket) error {
			_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, reopenAdmin)
			return err
		},
		"Closed, force-reopen by a requester": func(h *prioHarness, tk ticket.Ticket) error {
			h.policy = ticket.ReopenPolicyStaffAdmin
			_, err := h.svc.Reopen(context.Background(), tk.ID, h.newStatus.ID, ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser})
			return err
		},
		"Closed, requester reply": func(h *prioHarness, tk ticket.Ticket) error {
			_, err := h.svc.AddReply(context.Background(), tk.ID, "hello", false, true, "r@example.test",
				ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}, 30, h.newStatus.ID)
			return err
		},
		"Closed, status change to an inactive status": func(h *prioHarness, tk ticket.Ticket) error {
			h.policy = ticket.ReopenPolicyStaffAdmin
			inactive := h.inProgressStatus
			inactive.Active = false
			h.statuses.byName["In Progress"] = inactive
			_, err := h.svc.UpdateStatus(context.Background(), tk.ID, inactive.ID, reopenAdmin)
			return err
		},
	}
	for name, do := range cases {
		t.Run(name, func(t *testing.T) {
			h := newPrioHarness(t)
			tk := h.seedFinished("closed", ticket.PriorityMedium)
			before, _ := h.store.GetByID(context.Background(), tk.ID)
			rowsBefore := h.store.slaRows(tk.ID)

			require.Error(t, do(h, tk))

			after, _ := h.store.GetByID(context.Background(), tk.ID)
			require.Equal(t, before, after)
			require.Equal(t, rowsBefore, h.store.slaRows(tk.ID))
			require.Empty(t, h.auditStore.entries)
			require.Empty(t, h.dispatcher.events)
		})
	}
}

// Requirement 3, the domain half: the reopen is announced after the commit with
// who holds the ticket and who did it, so staff can be mailed and the actor
// skipped. A force-reopen keeps the requester's own mail.
func TestReopen_IsAnnouncedWithWhoHoldsTheTicketAndWhoDidIt(t *testing.T) {
	for _, d := range reopenDoors() {
		t.Run(d.name, func(t *testing.T) {
			h := newPrioHarness(t)
			tk := h.seedFinished(d.from, ticket.PriorityMedium)

			actor, err := d.do(h, tk)
			require.NoError(t, err)

			var n int
			for _, ev := range h.dispatcher.events {
				if ev.Type == notification.EventTicketReopened {
					n++
				}
			}
			require.Equal(t, 1, n, "announced once")
			ev := lastEventOfType(t, h.harness, notification.EventTicketReopened)
			require.Equal(t, &reopenAssignee, ev.AssigneeUserID)
			require.Equal(t, &reopenGroup, ev.AssigneeGroupID)
			require.Equal(t, actor.UserID, ev.ActorID)
			require.Equal(t, tk.ID, ev.TicketID)
		})
	}
}

// Only the door that already mailed the requester keeps doing so: a status
// change already tells the guest through its own event, and the requester who
// reopened needs no mail about their own reply.
func TestReopen_TheRequestersOwnMailIsUnchanged(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	tk.ReporterUserID, tk.GuestEmail = nil, ptrTo("guest@example.test")
	h.store.seed(tk)

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)

	reopened := lastEventOfType(t, h.harness, notification.EventTicketReopened)
	require.Empty(t, reopened.Recipient, "the status-change event already mails the guest; a second mail would be a duplicate")
	status := lastEventOfType(t, h.harness, notification.EventTicketStatusChanged)
	require.Equal(t, "guest@example.test", status.Recipient)
}

// A reply that reopens is announced by the reopen, so staff are not mailed
// twice for one action: the reply event no longer asks for staff mail.
func TestReopen_AReplyThatReopensDoesNotAlsoAskForTheReplyMail(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	requester := ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}

	_, err := h.svc.AddReply(context.Background(), tk.ID, "still broken", false, true, "r@example.test", requester, 30, h.newStatus.ID)
	require.NoError(t, err)

	reply := lastEventOfType(t, h.harness, notification.EventTicketReplied)
	require.False(t, reply.FromRequester, "the reopen event carries the staff mail for this action")
	require.Equal(t, "r@example.test", reply.Recipient, "everything else about the reply event is unchanged")

	// And a requester's reply that does not reopen still asks for it.
	open := h.seedMedium()
	open.ReporterUserID = tk.ReporterUserID
	h.store.seed(open)
	h.dispatcher.events = nil
	_, err = h.svc.AddReply(context.Background(), open.ID, "more detail", false, true, "r@example.test", requester, 30, h.newStatus.ID)
	require.NoError(t, err)
	require.True(t, lastEventOfType(t, h.harness, notification.EventTicketReplied).FromRequester)
}

func TestPriorityRaised(t *testing.T) {
	for in, want := range map[ticket.Priority]ticket.Priority{
		ticket.PriorityLow:      ticket.PriorityMedium,
		ticket.PriorityMedium:   ticket.PriorityHigh,
		ticket.PriorityHigh:     ticket.PriorityCritical,
		ticket.PriorityCritical: ticket.PriorityCritical,
	} {
		require.Equal(t, want, in.Raised(), string(in))
	}
}
