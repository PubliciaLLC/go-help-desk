package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Reclassifying a ticket (1.3.0 audit D3, the gap #468 left open). A policy is
// matched on priority and category, so a new category can mean a different
// policy; and a classification change is audited in the change's transaction.
//
// The SLA follows the rule a staff priority change already has (DESIGN.md → SLA
// Tracking → Re-matching): the policy is matched again, the clock keeps its
// start, and a target already met stays met. Only a reopen restarts a clock.
// Only an open ticket can be reclassified (see classifyInTx), so no finished
// ticket's SLA is ever re-matched.

type reclassifyHarness struct {
	*prioHarness
	oldCat, newCat uuid.UUID
	forNewCat      *uuid.UUID // the policy newCat matches, nil for none
}

func newReclassifyHarness(t *testing.T) *reclassifyHarness {
	t.Helper()
	h := &reclassifyHarness{prioHarness: newPrioHarness(t), oldCat: uuid.New(), newCat: uuid.New()}
	byCategory := uuid.New()
	h.forNewCat = &byCategory
	h.store.matchSLA = func(p ticket.Priority, cat uuid.UUID) *uuid.UUID {
		if cat == h.newCat {
			return h.forNewCat
		}
		return &h.medium
	}
	return h
}

func (h *reclassifyHarness) seed() ticket.Ticket {
	tk := h.seedMedium()
	tk.CategoryID = h.oldCat
	h.store.seed(tk)
	return tk
}

func reclassify(h *reclassifyHarness, id uuid.UUID, cat uuid.UUID, typ *uuid.UUID) (ticket.Ticket, error) {
	return h.svc.UpdateCTI(context.Background(), id, cat, typ, nil, priorityStaff)
}

func TestReclassify_AnotherPolicyMatchesAndTheClockIsKept(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seed()
	start := time.Now().Add(-90 * time.Minute).Truncate(time.Second)
	old := h.store.seedSLARecord(tk.ID, h.medium, &start, 40, metFacts())

	_, err := reclassify(h, tk.ID, h.newCat, nil)
	require.NoError(t, err)

	rows := h.store.slaRows(tk.ID)
	require.Len(t, rows, 2, "the old record is superseded and a new one starts; nothing is rewritten")
	require.Equal(t, old, rows[0].id)
	require.NotNil(t, rows[0].supersededAt)
	cur := rows[1]
	require.Nil(t, cur.supersededAt)
	require.Equal(t, *h.forNewCat, cur.policyID, "the policy for the new category")
	require.Equal(t, &start, cur.startedAt, "elapsed time still counts: a reclassify never restarts a clock")
	require.EqualValues(t, 40, cur.pausedBase)
	require.Equal(t, metFacts().FirstResponseAt.Unix(), cur.facts.FirstResponseAt.Unix(), "a target already met stays met")
}

func TestReclassify_IsAuditedWithTheSLAOutcome(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seed()
	old := h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})

	_, err := reclassify(h, tk.ID, h.newCat, nil)
	require.NoError(t, err)

	e := onlyEntry(t, h.auditStore.entries, "classified")
	require.Equal(t, "ticket", e.EntityType)
	require.Equal(t, tk.ID, e.EntityID)
	require.NotNil(t, e.ActorID)
	require.Equal(t, priorityActorID, *e.ActorID, "who reclassified it")
	require.Equal(t, h.oldCat, e.Before["category_id"])
	require.Equal(t, h.newCat, e.After["category_id"])
	require.Equal(t, ticket.SLARematched, e.After["sla_outcome"])
	require.Equal(t, old, e.Before["sla_record_id"])
	require.Equal(t, h.medium, e.Before["sla_policy_id"])
	require.Equal(t, *h.forNewCat, e.After["sla_policy_id"])
	require.Equal(t, h.store.slaRows(tk.ID)[1].id, e.After["sla_record_id"])
}

func TestReclassify_OutcomesFollowThePriorityTable(t *testing.T) {
	t.Run("no policy matches the new category: the record is superseded and the ticket shows no SLA", func(t *testing.T) {
		h := newReclassifyHarness(t)
		h.forNewCat = nil
		tk := h.seed()
		h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})

		_, err := reclassify(h, tk.ID, h.newCat, nil)
		require.NoError(t, err)

		rows := h.store.slaRows(tk.ID)
		require.Len(t, rows, 1)
		require.NotNil(t, rows[0].supersededAt)
		require.Equal(t, ticket.SLARemoved, onlyEntry(t, h.auditStore.entries, "classified").After["sla_outcome"])
	})

	t.Run("a ticket with no record gets one, its clock running from the ticket's creation", func(t *testing.T) {
		h := newReclassifyHarness(t)
		tk := h.seed()

		_, err := reclassify(h, tk.ID, h.newCat, nil)
		require.NoError(t, err)

		rows := h.store.slaRows(tk.ID)
		require.Len(t, rows, 1)
		require.Equal(t, *h.forNewCat, rows[0].policyID)
		require.Nil(t, rows[0].startedAt, "a nil start is the ticket's creation")
		require.Equal(t, ticket.SLAAttached, onlyEntry(t, h.auditStore.entries, "classified").After["sla_outcome"])
	})

	t.Run("the same policy matches: nothing is written to the SLA", func(t *testing.T) {
		h := newReclassifyHarness(t)
		h.forNewCat = &h.medium
		tk := h.seed()
		h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})

		_, err := reclassify(h, tk.ID, h.newCat, nil)
		require.NoError(t, err)

		require.Len(t, h.store.slaRows(tk.ID), 1)
		require.Nil(t, h.store.slaRows(tk.ID)[0].supersededAt)
		require.Equal(t, ticket.SLAUnchanged, onlyEntry(t, h.auditStore.entries, "classified").After["sla_outcome"])
	})

	t.Run("tracking is switched off: nothing is re-matched", func(t *testing.T) {
		h := newReclassifyHarness(t)
		h.svc.SetSLAEnabled(func(context.Context) bool { return false })
		tk := h.seed()
		h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})

		_, err := reclassify(h, tk.ID, h.newCat, nil)
		require.NoError(t, err)

		require.Len(t, h.store.slaRows(tk.ID), 1)
		require.Nil(t, h.store.slaRows(tk.ID)[0].supersededAt)
		require.Equal(t, ticket.SLAOff, onlyEntry(t, h.auditStore.entries, "classified").After["sla_outcome"])
	})
}

// A policy is matched on priority and category, so a change below the category
// cannot change the match. It is still a change, and is still audited.
func TestReclassify_ATypeOnlyChangeIsAuditedAndLeavesTheSLAAlone(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seed()
	h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})
	typ := uuid.New()

	_, err := reclassify(h, tk.ID, h.oldCat, &typ)
	require.NoError(t, err)

	e := onlyEntry(t, h.auditStore.entries, "classified")
	require.Equal(t, typ, e.After["type_id"])
	require.Nil(t, e.Before["type_id"])
	require.NotContains(t, e.After, "sla_outcome", "no re-match was attempted")
	require.Len(t, h.store.slaRows(tk.ID), 1)
}

func TestReclassify_AChangeThatChangesNothingWritesNothing(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seed()
	h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})

	_, err := reclassify(h, tk.ID, h.oldCat, nil)
	require.NoError(t, err)

	require.Empty(t, h.auditStore.entries)
	require.Len(t, h.store.slaRows(tk.ID), 1)
}

// Classification only happens to open tickets: a Resolved or Closed ticket
// refuses it, for every role, with the same 409 every other write to a finished
// ticket gets, and nothing is written (no category, no SLA row, no audit entry).
func TestReclassify_AResolvedOrClosedTicketIsRefusedAndNothingIsWritten(t *testing.T) {
	typ := uuid.New()
	for name, seed := range map[string]func(h *reclassifyHarness) ticket.Ticket{
		"resolved": func(h *reclassifyHarness) ticket.Ticket { return h.seedResolved(uuid.New()) },
		"closed":   func(h *reclassifyHarness) ticket.Ticket { return h.seedClosed() },
	} {
		for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
			for _, newCategory := range []bool{true, false} {
				what := "a type only"
				if newCategory {
					what = "a new category"
				}
				t.Run(name+" "+string(role)+" "+what, func(t *testing.T) {
					h := newReclassifyHarness(t)
					tk := seed(h)
					tk.Priority, tk.CategoryID = ticket.PriorityMedium, h.oldCat
					h.store.seed(tk)
					h.store.seedSLARecord(tk.ID, h.medium, nil, 0, metFacts())
					rows := h.store.slaRows(tk.ID)
					c := ticket.Classification{CategoryID: h.oldCat, TypeID: &typ}
					if newCategory {
						c = ticket.Classification{CategoryID: h.newCat}
					}
					id := uuid.New()
					actor := ticket.Actor{UserID: &id, Role: role}
					updatesBefore := h.store.updates

					_, err := h.svc.UpdateCTI(context.Background(), tk.ID, c.CategoryID, c.TypeID, c.ItemID, actor)
					require.ErrorIs(t, err, ticket.ErrClosed)
					_, err = h.svc.Update(context.Background(), tk.ID, ticket.Patch{CTI: &c}, actor)
					require.ErrorIs(t, err, ticket.ErrClosed)

					stored, _ := h.store.GetByID(context.Background(), tk.ID)
					require.Equal(t, h.oldCat, stored.CategoryID)
					require.Nil(t, stored.TypeID)
					require.Equal(t, rows, h.store.slaRows(tk.ID), "its SLA record is untouched")
					require.Empty(t, h.auditStore.entries, "a refused change writes no entry")
					require.Equal(t, updatesBefore, h.store.updates)
				})
			}
		}
	}
}

// Every open status takes it: the default one and the ones a ticket sits in
// while it is being worked.
func TestReclassify_AnOpenTicketInAnyOpenStatusIsReclassified(t *testing.T) {
	for name, statusOf := range map[string]func(h *reclassifyHarness) uuid.UUID{
		"new":         func(h *reclassifyHarness) uuid.UUID { return h.newStatus.ID },
		"in progress": func(h *reclassifyHarness) uuid.UUID { return h.inProgressStatus.ID },
		"pending":     func(h *reclassifyHarness) uuid.UUID { return h.pendingStatus.ID },
	} {
		t.Run(name, func(t *testing.T) {
			h := newReclassifyHarness(t)
			tk := h.seed()
			tk.StatusID = statusOf(h)
			h.store.seed(tk)

			got, err := reclassify(h, tk.ID, h.newCat, nil)
			require.NoError(t, err)
			require.Equal(t, h.newCat, got.CategoryID)
			onlyEntry(t, h.auditStore.entries, "classified")
		})
	}
}

// A request that moves the ticket out of Resolved and reclassifies it is one
// transaction: the status step runs first, so the classification finds an open
// ticket on the locked row.
func TestReclassify_ARequestThatReopensAndReclassifiesIsAllowed(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seedResolved(uuid.New())
	tk.Priority, tk.CategoryID = ticket.PriorityMedium, h.oldCat
	h.store.seed(tk)
	newStatus := h.newStatus.ID

	_, err := h.svc.Update(context.Background(), tk.ID, ticket.Patch{
		StatusID: &newStatus,
		CTI:      &ticket.Classification{CategoryID: h.newCat},
	}, priorityStaff)
	require.NoError(t, err)

	stored, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, h.newCat, stored.CategoryID)
}

// The audit entry is in the change's transaction: when it cannot be written the
// reclassification and the SLA rows are rolled back with it.
func TestReclassify_AFailedAuditEntryRollsEverythingBack(t *testing.T) {
	h := newReclassifyHarness(t)
	tk := h.seed()
	h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})
	rows := h.store.slaRows(tk.ID)
	h.auditStore.err = errStoreDown

	_, err := reclassify(h, tk.ID, h.newCat, nil)
	require.Error(t, err)

	stored, _ := h.store.GetByID(context.Background(), tk.ID)
	require.Equal(t, h.oldCat, stored.CategoryID, "the category is not left changed without its entry")
	require.Equal(t, rows, h.store.slaRows(tk.ID), "nor the SLA rows")
}

// PATCH with a category and a priority is one transaction and ends with one
// current record, matched on both.
func TestReclassify_APatchWithCategoryAndPriorityEndsWithOneCurrentRecord(t *testing.T) {
	h := newReclassifyHarness(t)
	both := uuid.New()
	h.store.matchSLA = func(p ticket.Priority, cat uuid.UUID) *uuid.UUID {
		switch {
		case cat == h.newCat && p == ticket.PriorityHigh:
			return &both
		case cat == h.newCat:
			return h.forNewCat
		}
		return &h.medium
	}
	tk := h.seed()
	h.store.seedSLARecord(tk.ID, h.medium, nil, 0, ticket.SLAFacts{})
	high := ticket.PriorityHigh

	_, err := h.svc.Update(context.Background(), tk.ID, ticket.Patch{
		CTI:      &ticket.Classification{CategoryID: h.newCat},
		Priority: &high,
	}, priorityStaff)
	require.NoError(t, err)

	var current []uuid.UUID
	for _, r := range h.store.slaRows(tk.ID) {
		if r.supersededAt == nil {
			current = append(current, r.policyID)
		}
	}
	require.Equal(t, []uuid.UUID{both}, current)
	require.Len(t, h.auditStore.entries, 2, "one entry for each change: classified, priority_changed")
}
