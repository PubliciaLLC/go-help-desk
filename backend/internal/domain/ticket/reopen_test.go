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
	require.Len(t, h.store.slaRows(tk.ID), 3, "a second record: the later reopen restarted the SLA too")
	require.Nil(t, onlyCurrent(h, tk.ID).supersededAt)
	require.Equal(t, ticket.SLAFacts{}, onlyCurrent(h, tk.ID).facts)

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

// ── the dance: a restart, then priorities with no policy (Erik, 2026-10-14) ──
//
// A ticket whose priority moves to one with no policy has no current SLA
// record; moving it back used to start a record "from the ticket's creation",
// which threw away a restart. The clock now resumes from the newest superseded
// record's start and pause base, without its met or breach facts, so the time
// spent on the no-policy priority still counts and the dance gains nothing.

func TestSLADance_AfterARestartTheClockResumesFromTheRestart(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	ctx := context.Background()
	before := time.Now()
	_, err := h.svc.UpdateStatus(ctx, tk.ID, h.inProgressStatus.ID, reopenStaff) // restart under high
	require.NoError(t, err)
	restart := h.store.slaRows(tk.ID)[1]
	require.NotNil(t, restart.startedAt)
	require.False(t, restart.startedAt.Before(before))
	// A pause total as of the restart, so the base is something to carry.
	h.store.slaRecords[1].pausedBase = 42
	restart = h.store.slaRows(tk.ID)[1]

	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityLow, reopenStaff) // no policy
	require.NoError(t, err)
	require.Nil(t, onlyCurrent(h, tk.ID), "no policy, no current record")
	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityHigh, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur)
	require.Equal(t, h.high, cur.policyID)
	require.NotNil(t, cur.startedAt, "NULL would put the clock back at the ticket's creation")
	require.True(t, restart.startedAt.Equal(*cur.startedAt), "resumes from the restart")
	require.Equal(t, int64(42), cur.pausedBase, "and its pause base")
	require.Equal(t, ticket.SLAFacts{}, cur.facts, "the superseded record's facts are not copied")
	pc := h.auditStore.entries[len(h.auditStore.entries)-1]
	require.Equal(t, ticket.SLAAttached, pc.After["sla_outcome"])
}

// A ticket that never had a record still starts from its creation.
func TestSLADance_ATicketThatNeverHadARecordStartsFromCreation(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedMedium() // open, no record
	_, err := h.svc.ChangePriority(context.Background(), tk.ID, ticket.PriorityHigh, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur)
	require.Nil(t, cur.startedAt, "NULL reads as the ticket's creation")
}

// Without a restart the dance resumes the original clock, which was from
// creation: same truth, still NULL.
func TestSLADance_WithoutARestartTheOriginalClockResumes(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedMedium()
	h.store.seedSLARecord(tk.ID, h.medium, nil, 7, ticket.SLAFacts{})
	ctx := context.Background()

	_, err := h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityLow, reopenStaff)
	require.NoError(t, err)
	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityHigh, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur)
	require.Nil(t, cur.startedAt)
	require.Equal(t, int64(7), cur.pausedBase)
}

func onlyCurrent(h *prioHarness, id uuid.UUID) *fakeSLARecord {
	for _, r := range h.store.slaRows(id) {
		if r.supersededAt == nil {
			r := r
			return &r
		}
	}
	return nil
}

// ── every reopen is a restart point, even onto a priority with no policy ─────
//
// (Erik, 2026-10-14.) A reopen that lands on a priority with no SLA policy
// starts no record, but the clock restarted all the same: the ticket remembers
// when it was last reopened and its pause total then, and a later change that
// gives it a record back resumes from the later of that and the newest
// superseded record's start.

// Scenario 1: medium has a policy, high has none. The first reopen raises the
// ticket to high (no record starts), and staff move it back to medium. The new
// record resumes from the reopen, not from before it, so the time the ticket sat
// Resolved does not count.
func TestReopenWithNoPolicy_AMoveBackResumesFromTheReopenNotFromBefore(t *testing.T) {
	h := newPrioHarness(t)
	delete(h.store.slaPolicies, ticket.PriorityHigh)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	tk.SLAPausedSeconds = 100 // the ticket's Pending total; the old record's base is 0
	h.store.seed(tk)
	ctx := context.Background()
	before := time.Now()

	_, err := h.svc.UpdateStatus(ctx, tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)
	require.Nil(t, onlyCurrent(h, tk.ID), "high has no policy: the reopen starts no record")

	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityMedium, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur)
	require.NotNil(t, cur.startedAt, "NULL, or the old record's start, would count the days spent Resolved")
	require.False(t, cur.startedAt.Before(before), "resumes from the reopen")
	require.Equal(t, int64(100), cur.pausedBase, "with the pause total as of the reopen, not the old record's base")
	require.Equal(t, ticket.SLAFacts{}, cur.facts)
	got, _ := h.store.GetByID(ctx, tk.ID)
	require.True(t, got.LastReopenedAt.Equal(*cur.startedAt))
}

// Scenario 2: the first reopen restarts the clock at T1; the ticket moves to a
// priority with no policy, is resolved, and is reopened at T2 (no record starts
// again). Moving back resumes from T2, not T1.
func TestReopenWithNoPolicy_ALaterReopenMovesTheResumePoint(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	ctx := context.Background()

	_, err := h.svc.UpdateStatus(ctx, tk.ID, h.inProgressStatus.ID, reopenStaff) // T1: high, restart
	require.NoError(t, err)
	t1 := *onlyCurrent(h, tk.ID).startedAt
	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityLow, reopenStaff) // no policy
	require.NoError(t, err)
	_, err = h.svc.Resolve(ctx, tk.ID, "done", reopenStaff)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	_, err = h.svc.UpdateStatus(ctx, tk.ID, h.inProgressStatus.ID, reopenStaff) // T2: no bump, no policy
	require.NoError(t, err)
	require.Nil(t, onlyCurrent(h, tk.ID))

	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityHigh, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur)
	require.True(t, cur.startedAt.After(t1), "T2, not T1")
	got, _ := h.store.GetByID(ctx, tk.ID)
	require.True(t, got.LastReopenedAt.Equal(*cur.startedAt))
}

// The record's own restart still wins when it is the later one: a reopen that
// starts a record, and nothing after it, resumes from that record.
func TestReopenWithNoPolicy_TheNewestRestartWins(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	ctx := context.Background()
	_, err := h.svc.UpdateStatus(ctx, tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)
	restart := *onlyCurrent(h, tk.ID).startedAt

	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityLow, reopenStaff)
	require.NoError(t, err)
	_, err = h.svc.ChangePriority(ctx, tk.ID, ticket.PriorityHigh, reopenStaff)
	require.NoError(t, err)

	require.True(t, restart.Equal(*onlyCurrent(h, tk.ID).startedAt))
}

// sla_restarted is written when a reopen supersedes a record and starts none
// (the old clock ended and the restart point is recorded on the ticket), and
// not when there was no clock at all.
func TestReopenWithNoPolicy_SLARestartedIsWrittenOnlyWhenARecordWasReplaced(t *testing.T) {
	t.Run("a record is superseded and none starts", func(t *testing.T) {
		h := newPrioHarness(t)
		delete(h.store.slaPolicies, ticket.PriorityHigh)
		tk := h.seedFinished("resolved", ticket.PriorityMedium)
		_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
		require.NoError(t, err)
		e := onlyEntry(t, h.auditStore.entries, "sla_restarted")
		require.NotContains(t, e.After, "sla_record_id")
		require.NotNil(t, e.After["previous_sla_record_id"])
	})
	t.Run("there was no record and none starts", func(t *testing.T) {
		h := newPrioHarness(t)
		tk := h.seedFinished("resolved", ticket.PriorityMedium)
		h.store.slaRecords = nil
		delete(h.store.slaPolicies, ticket.PriorityHigh)
		_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
		require.NoError(t, err)
		require.Zero(t, actionsOf(h, "sla_restarted"))
		got, _ := h.store.GetByID(context.Background(), tk.ID)
		require.NotNil(t, got.LastReopenedAt, "but the reopen is still a restart point")
	})
}

// A reopen that starts a FIRST clock on a ticket that had no record replaces
// nothing, so it writes no sla_restarted (Erik, 2026-10-15). The record itself
// starts, and priority_changed still says so.
func TestReopen_StartingAFirstClockWritesNoSLARestarted(t *testing.T) {
	h := newPrioHarness(t)
	tk := h.seedFinished("resolved", ticket.PriorityMedium)
	h.store.slaRecords = nil // the ticket never had a record

	_, err := h.svc.UpdateStatus(context.Background(), tk.ID, h.inProgressStatus.ID, reopenStaff)
	require.NoError(t, err)

	cur := onlyCurrent(h, tk.ID)
	require.NotNil(t, cur, "high has a policy: a clock starts")
	require.NotNil(t, cur.startedAt)
	require.Zero(t, actionsOf(h, "sla_restarted"), "nothing was replaced, so nothing restarted")
	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLAAttached, pc.After["sla_outcome"])
}

// Every door stamps the restart point, not only UpdateStatus: the instant, and
// the Pending total at that instant.
func TestReopen_EveryDoorStampsTheRestartPoint(t *testing.T) {
	for _, d := range reopenDoors() {
		t.Run(d.name, func(t *testing.T) {
			h := newPrioHarness(t)
			tk := h.seedFinished(d.from, ticket.PriorityMedium)
			tk.SLAPausedSeconds = 100
			h.store.seed(tk)
			before := time.Now()

			_, err := d.do(h, tk)
			require.NoError(t, err)

			got, _ := h.store.GetByID(context.Background(), tk.ID)
			require.NotNil(t, got.LastReopenedAt)
			require.False(t, got.LastReopenedAt.Before(before))
			require.Equal(t, int64(100), got.PausedSecondsAtLastReopen)
		})
	}
}
