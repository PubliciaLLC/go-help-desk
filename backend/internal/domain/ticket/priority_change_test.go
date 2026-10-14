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
// policy is re-matched, the clock keeps its start and a met target stays met; a
// Resolved or Closed ticket is refused; the change and its audit entry commit
// together. A record is never rewritten: a re-match supersedes it and starts a
// new one. Only a reopen restarts a clock (changePriorityInTx, reason
// reopened).

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

// metFacts is a record whose response target has been met, late: the facts a
// re-match must carry onto the record that replaces it.
func metFacts() ticket.SLAFacts {
	at := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	frozen := int64(3000)
	return ticket.SLAFacts{
		FirstResponseAt: &at, ResponseElapsedAtMetSeconds: &frozen, ResponseBreachedAt: &at,
	}
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
			h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, ticket.SLAFacts{})
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

// A Resolved or Closed ticket is refused outright, priority included, with the
// closed-ticket conflict every other write to a closed ticket gets. Even the
// same priority is refused: the answer does not depend on what was asked for.
func TestChangePriority_ResolvedAndClosedAreRefused(t *testing.T) {
	cases := []struct {
		name string
		seed func(h *prioHarness) ticket.Ticket
	}{
		{"resolved", func(h *prioHarness) ticket.Ticket { return h.seedResolved(uuid.New()) }},
		{"closed", func(h *prioHarness) ticket.Ticket { return h.seedClosed() }},
	}
	for _, tc := range cases {
		for _, p := range []ticket.Priority{ticket.PriorityHigh, ticket.PriorityMedium} {
			t.Run(tc.name+" to "+string(p), func(t *testing.T) {
				h := newPrioHarness(t)
				seeded := tc.seed(h)
				seeded.Priority = ticket.PriorityMedium
				h.store.seed(seeded)
				h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())
				rows := h.store.slaRows(seeded.ID)

				_, err := h.svc.ChangePriority(context.Background(), seeded.ID, p, priorityStaff)

				require.ErrorIs(t, err, ticket.ErrClosed)
				stored, _ := h.store.GetByID(context.Background(), seeded.ID)
				require.Equal(t, ticket.PriorityMedium, stored.Priority, "the priority is not changed")
				require.Zero(t, h.store.updates)
				require.Empty(t, h.auditStore.entries)
				require.Equal(t, rows, h.store.slaRows(seeded.ID), "the SLA record is not touched")
				require.Positive(t, h.store.forUpdateReads, "decided on the locked row")
			})
		}
	}
}

// The same priority is a double-submit: no write, no audit entry, no re-match.
func TestChangePriority_SamePriorityIsANoOp(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	got, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityMedium, priorityStaff)

	require.NoError(t, err)
	require.Equal(t, ticket.PriorityMedium, got.Priority)
	require.Zero(t, h.store.updates)
	require.Empty(t, h.auditStore.entries)
	require.Len(t, h.store.slaRows(seeded.ID), 1)
	require.Nil(t, h.store.slaRows(seeded.ID)[0].supersededAt)
}

// The same priority is not a trigger for a re-match either: a ticket with no
// record, under a priority a policy now covers, does not get one from a
// double-submit.
func TestChangePriority_SamePriorityDoesNotReMatchTheSLA(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium() // no record; a policy exists for medium

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityMedium, priorityStaff)

	require.NoError(t, err)
	require.Empty(t, h.store.slaRows(seeded.ID))
	require.Empty(t, h.auditStore.entries)
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

// A re-match is gated on the SLA toggle, and a toggle that was never wired
// reads as off.
func TestChangePriority_SLAOffLeavesTheRecordAlone(t *testing.T) {
	h := newPrioHarness(t)
	h.svc.SetSLAEnabled(func(context.Context) bool { return false })
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].supersededAt)
	require.Equal(t, ticket.SLAOff, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

// Erik's rule: a staff change NEVER restarts a clock. A record whose targets
// are met is superseded, never rewritten, and the new one keeps the clock start
// and the met targets; only the policy changes.
func TestChangePriority_StaffChangeNeverRestartsAMetRecord(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	oldStart := time.Now().Add(-5 * time.Hour)
	facts := metFacts()
	oldID := h.store.seedSLARecord(seeded.ID, h.medium, &oldStart, 30, facts)
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

	// The new one: current, under the new policy, the same clock, the same facts.
	cur := rows[1]
	require.Nil(t, cur.supersededAt)
	require.Equal(t, h.high, cur.policyID)
	require.NotEqual(t, oldID, cur.id)
	require.NotNil(t, cur.startedAt)
	require.True(t, cur.startedAt.Equal(oldStart), "the clock start is kept, not reset to the change")
	require.EqualValues(t, 30, cur.pausedBase)
	require.Equal(t, facts, cur.facts, "a target already met stays met")

	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLARematched, pc.After["sla_outcome"])
	require.Equal(t, h.medium, pc.Before["sla_policy_id"])
	require.Equal(t, oldID, pc.Before["sla_record_id"])
	require.Equal(t, h.high, pc.After["sla_policy_id"])
	require.Equal(t, cur.id, pc.After["sla_record_id"])
	require.Len(t, h.auditStore.entries, 1, "a staff change never writes sla_restarted")
}

func TestChangePriority_RunningRecordFromCreationStaysFromCreation(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, ticket.SLAFacts{})

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].supersededAt)
	require.Nil(t, rows[1].startedAt, "no start means ticket creation; inheriting that must not invent one")
	require.Equal(t, ticket.SLARematched, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

// The new priority maps to the record's own policy: nothing about the targets
// changed, so no new record (Q2).
func TestChangePriority_SamePolicyIsUnchanged(t *testing.T) {
	h := newPrioHarness(t)
	h.store.slaPolicies[ticket.PriorityHigh] = h.medium // a catch-all
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].supersededAt)
	require.Equal(t, ticket.SLAUnchanged, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
	require.Len(t, h.auditStore.entries, 1)
}

// No record yet and a policy matches the new priority: the clock started when
// the ticket was created, not when somebody changed the priority (Erik, Q4).
// It may be breached at once.
func TestChangePriority_NoRecordYetStartsOneFromTicketCreation(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.Equal(t, h.high, rows[0].policyID)
	require.Nil(t, rows[0].startedAt, "a nil start is measured from ticket creation, as every legacy record is")
	require.Zero(t, rows[0].pausedBase)
	require.Equal(t, ticket.SLAAttached, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

// No policy matches the new priority: the old record is superseded and the
// ticket shows no SLA (Q5).
func TestChangePriority_NoMatchingPolicySupersedesAndStartsNothing(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, ticket.SLAFacts{})

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityLow, priorityStaff)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].supersededAt)
	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.SLARemoved, pc.After["sla_outcome"])
	require.NotContains(t, pc.After, "sla_record_id")
}

// The change and its audit entry commit together, or not at all: failing the
// audit write, after the priority, the supersede and the new record have been
// written, must undo all of it.
func TestChangePriority_AuditFailureRollsEverythingBack(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	start := time.Now().Add(-time.Hour)
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, metFacts())
	rowsBefore := h.store.slaRows(seeded.ID)
	h.auditStore.failOnCall = 1

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)

	require.ErrorIs(t, err, errStoreDown)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityMedium, stored.Priority, "the priority is rolled back")
	require.Equal(t, rowsBefore, h.store.slaRows(seeded.ID), "the SLA rows are rolled back: no supersede, no new record")
	require.Empty(t, h.auditStore.entries)
	require.Equal(t, 1, h.atomic.rollbacks)
	require.Zero(t, h.atomic.commits)
}

// A store failure while writing the SLA also rolls the priority back.
func TestChangePriority_SLAWriteFailureRollsTheTicketBack(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	start := time.Now().Add(-time.Hour)
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, ticket.SLAFacts{})
	h.store.seedSLARecord(seeded.ID, h.medium, &start, 0, ticket.SLAFacts{}) // a second current row: impossible in Postgres

	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)

	require.ErrorIs(t, err, errSecondCurrentSLARecord)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityMedium, stored.Priority)
	require.Empty(t, h.auditStore.entries)
}

// ── The reopen path (#464): changePriorityInTx with reason reopened ──────────
//
// A requester's reply reopened the ticket, and its transaction calls
// changePriorityInTx(t.Priority.Raised(), reopened, ...) once, whatever the
// priority is. It checks no role (the actor is the requester) and no status
// (the caller has just moved the ticket out of Closed in this transaction), and
// it always restarts both clocks from now.

// inTx runs changePriorityInTx the way the reopen path does, inside a caller's
// transaction, as a reporting user.
func (h *prioHarness) reopenBump(t *testing.T, ticketID uuid.UUID, p ticket.Priority) (ticket.Actor, error) {
	t.Helper()
	requester := uuid.New()
	actor := ticket.Actor{UserID: &requester, Role: user.RoleUser}
	err := h.atomic.InTx(context.Background(), func(st ticket.Store, au audit.Store) error {
		tk, err := st.GetByIDForUpdate(context.Background(), ticketID)
		require.NoError(t, err)
		_, err = h.svc.ChangePriorityInTx(context.Background(), st, au, tk, p,
			ticket.PriorityChangeReopened, actor, time.Now())
		return err
	})
	return actor, err
}

// (a) A reopen that raises the priority: the priority is written, and both
// clocks start over from now on a new record. The old one is kept, superseded.
func TestReopenBump_RaisingThePriorityRestartsBothClocks(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	oldStart := time.Now().Add(-5 * time.Hour)
	oldID := h.store.seedSLARecord(seeded.ID, h.medium, &oldStart, 30, metFacts())
	want := h.store.slaRows(seeded.ID)[0]

	before := time.Now()
	actor, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)

	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityHigh, stored.Priority)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)
	old := rows[0]
	require.NotNil(t, old.supersededAt)
	old.supersededAt = nil
	require.Equal(t, want, old, "the old record is kept, facts byte-identical")

	cur := rows[1]
	require.Nil(t, cur.supersededAt)
	require.Equal(t, h.high, cur.policyID)
	require.NotNil(t, cur.startedAt)
	require.False(t, cur.startedAt.Before(before), "the new clock starts now")
	require.Equal(t, ticket.SLAFacts{}, cur.facts, "both targets are timed anew")

	pc := onlyEntry(t, h.auditStore.entries, "priority_changed")
	require.Equal(t, ticket.PriorityChangeReopened, pc.After["priority_change_reason"])
	require.Equal(t, ticket.SLARestarted, pc.After["sla_outcome"])
	require.Equal(t, actor.UserID, pc.ActorID)

	sr := onlyEntry(t, h.auditStore.entries, "sla_restarted")
	require.Equal(t, map[string]any{"sla_record_id": oldID, "sla_policy_id": h.medium}, sr.Before)
	require.Equal(t, map[string]any{
		"sla_record_id": cur.id, "sla_policy_id": h.high, "previous_sla_record_id": oldID,
	}, sr.After)
	require.Empty(t, h.dispatcher.events)
}

// (b) A reopen at the highest priority, which cannot be raised: the same call,
// the same value. The priority does not change, so nothing is written to the
// ticket and there is no priority_changed entry, but both clocks still start
// over and sla_restarted says so.
func TestReopenBump_AtTheSamePriorityStillRestartsBothClocks(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	before := time.Now()
	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityMedium)
	require.NoError(t, err)

	require.Zero(t, h.store.updates, "the ticket row is not rewritten")
	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].supersededAt)
	require.Equal(t, h.medium, rows[1].policyID, "the same policy: restarted anyway")
	require.NotNil(t, rows[1].startedAt)
	require.False(t, rows[1].startedAt.Before(before))
	require.Equal(t, ticket.SLAFacts{}, rows[1].facts)

	require.Len(t, h.auditStore.entries, 1)
	onlyEntry(t, h.auditStore.entries, "sla_restarted")
}

func TestReopenBump_RunningRecordIsRestartedToo(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	oldStart := time.Now().Add(-5 * time.Hour)
	h.store.seedSLARecord(seeded.ID, h.medium, &oldStart, 0, ticket.SLAFacts{})

	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 2)
	require.False(t, rows[1].startedAt.Equal(oldStart), "the clock does not carry over on a reopen")
}

// The Resolved/Closed refusal and the same-priority no-op belong to a staff
// change. A reopen has just moved the ticket out of Closed in its own
// transaction, so it is not asked, whatever the row looks like to this call.
func TestReopenBump_IsNotRefusedByTheClosedRule(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedClosed()
	seeded.Priority = ticket.PriorityMedium
	h.store.seed(seeded)
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)
	require.Len(t, h.store.slaRows(seeded.ID), 2)
}

// With the SLA switched off no record is created or superseded; the priority
// still changes, and at an unchanged priority nothing at all is written.
func TestReopenBump_SLAOffCreatesNoRecord(t *testing.T) {
	h := newPrioHarness(t)
	h.svc.SetSLAEnabled(func(context.Context) bool { return false })
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityHigh, stored.Priority)
	require.Len(t, h.store.slaRows(seeded.ID), 1)
	require.Nil(t, h.store.slaRows(seeded.ID)[0].supersededAt)
	require.Equal(t, ticket.SLAOff, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
	require.Len(t, h.auditStore.entries, 1)

	_, err = h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)
	require.Len(t, h.auditStore.entries, 1, "nothing changed, nothing is written")
	require.Len(t, h.store.slaRows(seeded.ID), 1)
}

func TestReopenBump_NoRecordStartsOneNow(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()

	before := time.Now()
	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)

	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].startedAt)
	require.False(t, rows[0].startedAt.Before(before))
	require.Equal(t, ticket.SLAAttached, onlyEntry(t, h.auditStore.entries, "priority_changed").After["sla_outcome"])
}

func TestReopenBump_RejectsAnInvalidPriority(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	_, err := h.reopenBump(t, seeded.ID, "urgent")
	require.ErrorIs(t, err, ticket.ErrValidation)
	require.Empty(t, h.auditStore.entries)
}

// The reopen's SLA restart is part of its transaction: failing the second
// audit entry, after the priority, the supersede, the new record and the first
// entry, undoes all of it.
func TestReopenBump_AuditFailureRollsEverythingBack(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())
	rowsBefore := h.store.slaRows(seeded.ID)
	h.auditStore.failOnCall = 2

	_, err := h.reopenBump(t, seeded.ID, ticket.PriorityHigh)

	require.ErrorIs(t, err, errStoreDown)
	stored, _ := h.store.GetByID(context.Background(), seeded.ID)
	require.Equal(t, ticket.PriorityMedium, stored.Priority)
	require.Equal(t, rowsBefore, h.store.slaRows(seeded.ID))
	require.Empty(t, h.auditStore.entries)
}

// started_at is what tells a restart from a re-match, which is what the "SLA
// restarted" badge reads (sla.EarlierRecord, #443, Erik 2026-10-14). The facts
// are copied by a re-match, so they cannot: only a restart gives a record a
// started_at of its own. Pinned here, on the writer, so the reader's rule
// cannot drift from it.
func TestChangePriority_OnlyARestartGivesARecordItsOwnStartedAt(t *testing.T) {
	h := newPrioHarness(t)
	seeded := h.seedMedium()
	h.store.seedSLARecord(seeded.ID, h.medium, nil, 0, metFacts())

	// A staff re-match of a met, breached record from creation: started_at stays
	// NULL, though the facts are copied, and again on the way back.
	_, err := h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityHigh, priorityStaff)
	require.NoError(t, err)
	_, err = h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityMedium, priorityStaff)
	require.NoError(t, err)
	rows := h.store.slaRows(seeded.ID)
	require.Len(t, rows, 3)
	for i, r := range rows {
		require.Nil(t, r.startedAt, "record %d: a re-match copies the NULL start and invents none", i)
		require.Equal(t, metFacts().FirstResponseAt.Unix(), r.facts.FirstResponseAt.Unix(), "record %d: the facts are copied", i)
	}

	// A reopen: the new record has a started_at of its own and no facts.
	_, err = h.reopenBump(t, seeded.ID, ticket.PriorityHigh)
	require.NoError(t, err)
	rows = h.store.slaRows(seeded.ID)
	require.Len(t, rows, 4)
	restarted := rows[3]
	require.NotNil(t, restarted.startedAt)
	require.Equal(t, ticket.SLAFacts{}, restarted.facts)

	// A re-match after the restart carries that start over unchanged.
	_, err = h.svc.ChangePriority(context.Background(), seeded.ID, ticket.PriorityMedium, priorityStaff)
	require.NoError(t, err)
	rows = h.store.slaRows(seeded.ID)
	require.Len(t, rows, 5)
	require.NotNil(t, rows[4].startedAt)
	require.True(t, rows[4].startedAt.Equal(*restarted.startedAt), "a re-match keeps the restart's start")
}
