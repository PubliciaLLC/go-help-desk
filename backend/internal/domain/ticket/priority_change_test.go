package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// ChangePriority (#443b): staff and admin change a ticket's priority; the SLA
// is re-matched from the moment of the change; the change and its audit entries
// commit together. A record is never rewritten: a re-match supersedes it and
// starts a new one.

var (
	priorityActorID = uuid.New()
	priorityStaff   = ticket.Actor{UserID: &priorityActorID, Role: user.RoleStaff}
)

// prioHarness is a harness with the SLA switched on and one policy per priority
// (medium and high), plus helpers to seed the ticket's current record.
type prioHarness struct {
	*harness
	medium, high uuid.UUID // policy ids
}

func newPrioHarness(t *testing.T) *prioHarness {
	t.Helper()
	h := &prioHarness{harness: newHarness(t), medium: uuid.New(), high: uuid.New()}
	h.store.slaPolicies = map[ticket.Priority]uuid.UUID{
		ticket.PriorityMedium: h.medium,
		ticket.PriorityHigh:   h.high,
	}
	h.svc.SetSLAEnabled(func(context.Context) bool { return true })
	return h
}

// seedMedium plants an open medium-priority ticket.
func (h *prioHarness) seedMedium() ticket.Ticket {
	t := h.seedOpen()
	t.Priority = ticket.PriorityMedium
	h.store.seed(t)
	return t
}

// onlyEntry returns the single audit entry with the action, failing if there is
// not exactly one.
func onlyEntry(t *testing.T, entries []audit.Entry, action string) audit.Entry {
	t.Helper()
	var got []audit.Entry
	for _, e := range entries {
		if e.Action == action {
			got = append(got, e)
		}
	}
	require.Len(t, got, 1, "audit entries with action %q", action)
	return got[0]
}

func TestChangePriority_OnlyStaffAndAdmin(t *testing.T) {
	cases := []struct {
		name string
		role user.Role
		want error
	}{
		{"user is refused", user.RoleUser, ticket.ErrForbidden},
		{"an unknown role is refused", user.Role("auditor"), ticket.ErrForbidden},
		{"staff may", user.RoleStaff, nil},
		{"admin may", user.RoleAdmin, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPrioHarness(t)
			seeded := h.seedMedium()
			h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, false)
			id := uuid.New()

			_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh,
				ticket.Actor{UserID: &id, Role: tc.role})

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Zero(t, h.store.updates, "a refused change writes nothing")
				require.Empty(t, h.auditStore.entries)
				require.Len(t, h.store.slaRows(seeded.ID), 1, "the SLA record is untouched")
				stored, _ := h.store.GetByID(context.Background(), seeded.ID)
				require.Equal(t, ticket.PriorityMedium, stored.Priority)
				return
			}
			require.NoError(t, err)
			stored, _ := h.store.GetByID(context.Background(), seeded.ID)
			require.Equal(t, ticket.PriorityHigh, stored.Priority)
		})
	}
}

func TestChangePriority_RejectsAnInvalidPriority(t *testing.T) {
	for _, p := range []ticket.Priority{"urgent", "", "HIGH"} {
		t.Run(string(p), func(t *testing.T) {
			h := newPrioHarness(t)
			seeded := h.seedMedium()

			_, err := h.svc.ChangePriority(context.Background(), seeded.ID, p, priorityStaff)

			require.ErrorIs(t, err, ticket.ErrValidation)
			require.Zero(t, h.store.updates)
			require.Empty(t, h.auditStore.entries)
		})
	}
}

func TestChangePriority_UnknownTicket(t *testing.T) {
	h := newPrioHarness(t)
	_, err := h.svc.ChangePriority(context.Background(), uuid.New(), ticket.PriorityHigh, priorityStaff)
	require.ErrorIs(t, err, errNotFound)
}

// The same priority is a double-submit: no write, no audit entry, no re-match.
func TestChangePriority_SamePriorityIsANoOp(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, true)

	got, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityMedium, priorityStaff)

	require.NoError(t, err)
	require.Equal(t, ticket.PriorityMedium, got.Priority)
	require.Zero(t, h.store.updates)
	require.Empty(t, h.auditStore.entries)
	require.Len(t, h.store.slaRows(seeded.ID), 1)
	require.Nil(t, h.store.slaRows(seeded.ID)[0].supersededAt)
}

// A priority change announces nothing: there is no event for it (decided on
// #468, Q9), and #413's priority filter must never see one.
func TestChangePriority_DispatchesNoNotification(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)

	require.NoError(t, err)
	require.Empty(t, h.dispatcher.events)
}

func TestChangePriority_WritesThePriorityAndOneAuditEntry(t *testing.T) {
	h := newHarness(t) // SLA never switched on: sla_off
	seeded := h.seedOpen()
	seeded.Priority = ticket.PriorityMedium
	h.store.seed(seeded)

	before := time.Now()
	got, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityCritical, priorityStaff)
	require.NoError(t, err)

	require.Equal(t, ticket.PriorityCritical, got.Priority)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityCritical, stored.Priority)
	require.False(t, stored.UpdatedAt.Before(before), "updated_at moves with the change")

	require.Len(t, h.auditStore.entries, 1)
	e := h.auditStore.entries[0]
	require.Equal(t, "priority_changed", e.Action)
	require.Equal(t, "ticket", e.EntityType)
	require.Equal(t, seeded.ID, e.EntityID)
	require.Equal(t, &priorityActorID, e.ActorID)
	require.Equal(t, ticket.PriorityMedium, e.Before["priority"])
	require.Equal(t, ticket.PriorityCritical, e.After["priority"])
	require.Equal(t, ticket.PriorityChangeManual, e.After["priority_change_reason"])
	require.Equal(t, ticket.SLAOff, e.After["sla_outcome"])
}

// The Q6 / design default: a re-match is gated on the SLA toggle, and a
// toggle that was never wired reads as off.
func TestChangePriority_SLAOffLeavesTheRecordAlone(t *testing.T) {
	h := newPrioHarness(t)
	h.svc.SetSLAEnabled(func(context.Context) bool { return false })
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, true)

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].supersededAt)
	require.Equal(t, ticket.SLAOff, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

// Resolved and Closed tickets may change priority, but the SLA is not
// re-matched: restarting a clock on a ticket nobody is working would breach it
// while it sits there (Q3).
func TestChangePriority_ResolvedAndClosedKeepTheirSLA(t *testing.T) {
	cases := []struct {
		name string
		seed func(h *prioHarness) ticket.Ticket
	}{
		{"resolved", func(h *prioHarness) ticket.Ticket { return h.seedResolved(uuid.New()) }},
		{"closed", func(h *prioHarness) ticket.Ticket { return h.seedClosed() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newPrioHarness(t)
			seeded := tc.seed(h)
			seeded.Priority = ticket.PriorityMedium
			h.store.seed(seeded)
			h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, true)

			got, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
			require.NoError(t, err)
			require.Equal(t, ticket.PriorityHigh, got.Priority)

			rows := h.store.slaRows(seeded.ID)
			require.Len(t, rows, 1, "no second record")
			require.Nil(t, rows[0].supersededAt, "the record is not superseded")
			require.Equal(t, h.medium, rows[0].policyID)
			e := onlyEntry(t, h.auditStore.entries, "priority_changed")
			require.Equal(t, ticket.SLANotOpen, e.After["sla_outcome"])
			require.Len(t, h.auditStore.entries, 1, "no sla_restarted")
		})
	}
}

// A met or breached record is superseded, never rewritten, and a new one times
// both targets from the change. sla_restarted is its own entry so the audit
// view's action filter finds it.
func TestChangePriority_RestartSupersedesTheSettledRecord(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	oldStart := time.Now().Add(-5 * time.Hour)
	oldID := h.store.seedSLARecord(seeded.ID, h.medium, &oldStart, 30, true)
	want := h.store.slaRows(seeded.ID)[0]

	before := time.Now()
	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)
	after := time.Now()

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)

	// The old record: every fact as it was; only superseded_at changed.
	old := rows[0]
	require.NotNil(t, old.supersededAt)
	require.False(t, old.supersededAt.Before(before))
	require.False(t, old.supersededAt.After(after))
	old.supersededAt = nil
	require.Equal(t, want, old, "a superseded record's facts are byte-identical")

	// The new one: current, under the new policy, and its clock starts now.
	cur := rows[1]
	require.Nil(t, cur.supersededAt)
	require.Equal(t, h.high, cur.policyID)
	require.NotEqual(t, oldID, cur.id)
	require.NotNil(t, cur.startedAt, "a restart records its start")
	require.False(t, cur.startedAt.Before(before))
	require.False(t, cur.startedAt.After(after))
	require.False(t, cur.settled)

	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLARestarted, pc.After["sla_outcome"])
	require.Equal(t, h.medium, pc.Before["sla_policy_id"])
	require.Equal(t, oldID, pc.Before["sla_record_id"])
	require.Equal(t, h.high, pc.After["sla_policy_id"])
	require.Equal(t, cur.id, pc.After["sla_record_id"])

	sr := onlyEntry(t, h.auditStore.entries, "sla_restarted")
	require.Equal(t, "ticket", sr.EntityType)
	require.Equal(t, seeded.ID, sr.EntityID)
	require.Equal(t, &priorityActorID, sr.ActorID)
	require.Equal(t, map[string]any{"sla_record_id": oldID, "sla_policy_id": h.medium}, sr.Before)
	require.Equal(t, map[string]any{
		"sla_record_id": cur.id, "sla_policy_id": h.high, "previous_sla_record_id": oldID,
	}, sr.After)
}

// A running record is re-matched without losing the time it has already spent:
// the new row inherits the old row's clock start and pause base.
func TestChangePriority_RunningRecordKeepsItsClock(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	oldStart := time.Now().Add(-3 * time.Hour)
	oldID := h.store.seedSLARecord(seeded.ID, h.medium, &oldStart, 120, false)

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].supersededAt)
	cur := rows[1]
	require.Nil(t, cur.supersededAt)
	require.Equal(t, h.high, cur.policyID)
	require.NotNil(t, cur.startedAt)
	require.True(t, cur.startedAt.Equal(oldStart), "the clock start is inherited, not reset to now")
	require.EqualValues(t, 120, cur.pausedBase)

	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLARematched, pc.After["sla_outcome"])
	require.Equal(t, oldID, pc.Before["sla_record_id"])
	require.Len(t, h.auditStore.entries, 1, "only a restart writes sla_restarted")
}

func TestChangePriority_RunningRecordFromCreationStaysFromCreation(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, false)

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	cur := h.store.slaRows(seeded.ID)[1]
	require.Nil(t, cur.startedAt, "no start means ticket creation; inheriting that must not invent one")
}

// The new priority maps to the record's own policy: nothing about the targets
// changed, so a restart would only clear a breach (Q2).
func TestChangePriority_SamePolicyDoesNotRestart(t *testing.T) {
	h := newPrioHarness(t)
	h.store.slaPolicies[ticket.PriorityHigh] = h.medium // a catch-all
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, true)

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].supersededAt)
	require.Equal(t, ticket.SLAUnchanged, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
	require.Len(t, h.auditStore.entries, 1)
}

// No record yet and a policy matches the new priority: the clock starts at the
// change (Q4).
func TestChangePriority_NoRecordYetStartsOneAtTheChange(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()

	before := time.Now()
	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Equal(t, h.high, rows[0].policyID)
	require.NotNil(t, rows[0].startedAt)
	require.False(t, rows[0].startedAt.Before(before))
	require.Equal(t, ticket.SLAAttached, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

// No policy matches the new priority: the old record is superseded and the
// ticket shows no SLA (Q5).
func TestChangePriority_NoMatchingPolicySupersedesAndStartsNothing(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, false)

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityLow, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].supersededAt)
	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLARemoved, pc.After["sla_outcome"])
	require.NotContains(t, pc.After, "sla_record_id")
}

// The change and its audit entries commit together, or not at all. The second
// case fails the SECOND audit write, after the priority, the supersede and the
// new record have all been written and the first entry accepted: everything
// must be undone.
func TestChangePriority_AuditFailureRollsEverythingBack(t *testing.T) {
	for _, failOn := range []int{1, 2} {
		t.Run(map[int]string{1: "priority_changed entry", 2: "sla_restarted entry"}[failOn], func(t *testing.T) {
			h := newPrioHarness(t)
			seeded := h.seedMedium()
			start := time.Now().Add(-time.Hour)
			h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, true)
			rowsBefore := h.store.slaRows(seeded.ID)
			h.auditStore.failOnCall = failOn

			_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)

			require.ErrorIs(t, err, errStoreDown)
			stored, _ := h.store.GetByID(context.Background(), seeded.ID)
			require.Equal(t, ticket.PriorityMedium, stored.Priority, "the priority is rolled back")
			require.Equal(t, rowsBefore, h.store.slaRows(seeded.ID), "the SLA rows are rolled back: no supersede, no new record")
			require.Empty(t, h.auditStore.entries, "no audit entry survives")
			require.Equal(t, 1, h.atomic.rollbacks)
			require.Zero(t, h.atomic.commits)
		})
	}
}

// A store failure while writing the SLA also rolls the priority back.
func TestChangePriority_SLAWriteFailureRollsTheTicketBack(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	// A current record the fake does not know is superseded, so StartSLARecord
	// finds the unique-current rule violated.
	start := time.Now().Add(-time.Hour)
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, true)
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, true) // a second current row: impossible in Postgres

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)

	require.ErrorIs(t, err, errSecondCurrentSLARecord)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityMedium, stored.Priority)
	require.Empty(t, h.auditStore.entries)
}

// changePriorityInTx is what #464's reopen path calls from inside its own
// transaction. It checks no role: the caller decided this change is allowed,
// and the actor there is the requester whose reply reopened the ticket.
func TestChangePriorityInTx_ForTheReopenPath(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	start := time.Now().Add(-time.Hour)
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, true)
	requester := uuid.New()
	actor := ticket.Actor{UserID: &requester, Role: user.RoleUser}

	err := h.atomic.InTx(context.Background(), func(st ticket.Store, au audit.Store) error {
		tk, err := st.GetByIDForUpdate(context.Background(), seeded.ID)
		require.NoError(t, err)
		_, err = h.svc.ChangePriorityInTx(context.Background(), st, au, tk, ticket.PriorityHigh,
			ticket.PriorityChangeReopened, actor, time.Now())
		return err
	})
	require.NoError(t, err)

	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityHigh, stored.Priority)
	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.PriorityChangeReopened, pc.After["priority_change_reason"])
	require.Equal(t, &requester, pc.ActorID)
	onlyEntry(t, h.auditStore.entries, "sla_restarted")
	require.Len(t, h.store.slaRows(seeded.ID), 2)
}

// Raising a priority that is already as high as it goes asks for the same
// value: a no-op in the transaction, so no SLA restart either (§2.6).
func TestChangePriorityInTx_SamePriorityIsANoOp(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, true)

	err := h.atomic.InTx(context.Background(), func(st ticket.Store, au audit.Store) error {
		tk, err := st.GetByIDForUpdate(context.Background(), seeded.ID)
		require.NoError(t, err)
		_, err = h.svc.ChangePriorityInTx(context.Background(), st, au, tk, ticket.PriorityMedium,
			ticket.PriorityChangeReopened, ticket.SystemActor, time.Now())
		return err
	})
	require.NoError(t, err)
	require.Empty(t, h.auditStore.entries)
	require.Len(t, h.store.slaRows(seeded.ID), 1)
}
