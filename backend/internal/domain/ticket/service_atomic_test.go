package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
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
				return h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor)
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
		{
			name: "ResolveAsDuplicate",
			run: func(h *harness) error {
				source := h.seedOpen()
				target := h.seedOpen()
				_, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "custom notes",
					ticket.Actor{UserID: &agent, Role: user.RoleStaff})
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

func TestResolveAsDuplicate_CreatesLinkAndResolvesTicket(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()

	result, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "duplicate of this",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.NoError(t, err)
	require.Equal(t, h.resolvedStatus.ID, result.StatusID)
	require.NotNil(t, result.ResolutionNotes)
	require.Equal(t, "duplicate of this", *result.ResolutionNotes)
	require.NotNil(t, result.ResolvedAt)

	// Verify link was created
	links, _ := h.store.ListLinks(context.Background(), source.ID)
	require.Len(t, links, 1)
	require.Equal(t, source.ID, links[0].SourceTicketID)
	require.Equal(t, target.ID, links[0].TargetTicketID)
	require.Equal(t, ticket.LinkDuplicateOf, links[0].LinkType)

	// Verify history entry was created
	require.Len(t, h.store.history, 1)
	require.Equal(t, source.ID, h.store.history[0].TicketID)
	require.Equal(t, h.resolvedStatus.ID, h.store.history[0].ToStatusID)

	// Verify audit entry was created with "resolved" action
	require.Len(t, h.auditStore.entries, 1)
	require.Equal(t, "resolved", h.auditStore.entries[0].Action)

	// Both events should be dispatched
	require.Len(t, h.dispatcher.events, 2)
}

func TestResolveAsDuplicate_DefaultsTheNotes(t *testing.T) {
	cases := []struct {
		name  string
		notes string
	}{
		{"empty", ""},
		{"whitespace", "  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			source := h.seedOpen()
			target := h.seedOpen()
			agent := uuid.New()

			result, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, tc.notes,
				ticket.Actor{UserID: &agent, Role: user.RoleStaff})

			require.NoError(t, err)
			expected := ticket.DuplicateResolutionNotes(target.TrackingNumber)
			require.Equal(t, expected, *result.ResolutionNotes)
		})
	}
}

func TestResolveAsDuplicate_UserMayNotResolve(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	userID := uuid.New()

	_, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "dup",
		ticket.Actor{UserID: &userID, Role: user.RoleUser})

	require.ErrorIs(t, err, ticket.ErrForbidden)
	// Verify nothing was written
	require.Equal(t, 0, h.store.updates)
	links, _ := h.store.ListLinks(context.Background(), source.ID)
	require.Empty(t, links)
	require.Equal(t, 0, h.atomic.rollbacks) // error was before InTx
}

func TestResolveAsDuplicate_AuditFailureRollsBackTheLink(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()
	h.auditStore.err = errStoreDown

	_, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "dup",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	// Ticket status should not have changed
	stored, _ := h.store.GetByID(context.Background(), source.ID)
	require.Equal(t, h.newStatus.ID, stored.StatusID)
	require.Nil(t, stored.ResolvedAt)
	// Link should not have been created
	links, _ := h.store.ListLinks(context.Background(), source.ID)
	require.Empty(t, links)
	// Should have rolled back
	require.Equal(t, 1, h.atomic.rollbacks)
}

func TestResolveAsDuplicate_SelfLinkRefused(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	agent := uuid.New()

	_, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, source.ID, "dup",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot link a ticket to itself")
	// #192: must map to 400, not 500 — which requires the typed sentinel,
	// not just matching text.
	require.ErrorIs(t, err, ticket.ErrSelfLink)
}

// TestResolveAsDuplicate_AlreadyLinkedIsSatisfiedNotConflict pins #194: if the
// exact duplicate_of link already exists (e.g. staff linked A duplicate-of B
// earlier without checking the resolve box, or a stale second tab), resolving
// must still succeed with the given notes rather than aborting with a
// conflict.
func TestResolveAsDuplicate_AlreadyLinkedIsSatisfiedNotConflict(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	// Staff already linked the two tickets without resolving.
	require.NoError(t, h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkDuplicateOf, staff))

	result, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "resolved on second pass", staff)

	require.NoError(t, err, "an already-existing identical link must not abort the resolve")
	require.Equal(t, h.resolvedStatus.ID, result.StatusID)
	require.NotNil(t, result.ResolutionNotes)
	require.Equal(t, "resolved on second pass", *result.ResolutionNotes)

	// Still exactly one link — the pre-existing row, not a duplicate of it.
	links, err := h.store.ListLinks(context.Background(), source.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, ticket.LinkDuplicateOf, links[0].LinkType)

	// #229: AddLink above already dispatched its own EventTicketLinked for
	// this pair. This resolve created no new link, so it must not dispatch a
	// second one — only the resolve's own EventTicketResolved.
	require.Equal(t, 1, countType(h.dispatcher.events, notification.EventTicketLinked),
		"AddLink's own dispatch, not a second one from the resolve that found nothing new to link")
	require.Equal(t, 1, countType(h.dispatcher.events, notification.EventTicketResolved))
}

// TestResolveAsDuplicate_ReResolvePreservesOriginalSLAInstant pins #234:
// ResolveAsDuplicate still passed a bare now to RecordResolved. A source
// ticket already Resolved, already linked to the same target (so this call's
// own no-op check rests on notes alone, same as Resolve), re-resolved with
// different notes genuinely re-executes (#225) and applyStatusTimestamps
// preserves the ticket's ORIGINAL ResolvedAt — the SLA call must use that
// same original instant, not this call's now.
func TestResolveAsDuplicate_ReResolvePreservesOriginalSLAInstant(t *testing.T) {
	h := newHarness(t)
	source := h.seedResolved(uuid.New())
	originalResolvedAt := *source.ResolvedAt
	target := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	require.NoError(t, h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkDuplicateOf, staff))

	_, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID,
		"resolved again, different notes", staff)
	require.NoError(t, err)

	require.Equal(t, 1, h.sla.resolutions)
	require.True(t, originalResolvedAt.Equal(h.sla.lastResolvedAt),
		"a re-resolve via ResolveAsDuplicate (#225) must repair/record the SLA resolution at the ticket's real original instant (#234), not this call's now")
}

// TestResolveAsDuplicate_SamePairDifferentTypeIsUnaffected pins the other half
// of #194: a link between the same two tickets but of a DIFFERENT type must
// not be special-cased — it is unrelated to the duplicate_of link this call
// creates, so both must end up existing.
func TestResolveAsDuplicate_SamePairDifferentTypeIsUnaffected(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	require.NoError(t, h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkRelatedTo, staff))

	result, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "actually a duplicate", staff)
	require.NoError(t, err)
	require.Equal(t, h.resolvedStatus.ID, result.StatusID)

	links, err := h.store.ListLinks(context.Background(), source.ID)
	require.NoError(t, err)
	require.Len(t, links, 2, "the related_to link and the new duplicate_of link must both exist")
	types := map[ticket.LinkType]bool{}
	for _, l := range links {
		types[l.LinkType] = true
	}
	require.True(t, types[ticket.LinkRelatedTo])
	require.True(t, types[ticket.LinkDuplicateOf])
}

func TestDuplicateResolutionNotes(t *testing.T) {
	tn := ticket.TrackingNumber("GHD-2026-000042")
	notes := ticket.DuplicateResolutionNotes(tn)
	require.Equal(t, "Duplicate of GHD-2026-000042", notes)
}
