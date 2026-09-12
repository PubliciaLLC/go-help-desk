package ticket_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The ticket lifecycle is the product. These tests cover the rules that decide
// who may move a ticket where, and what the system records when it does —
// the transitions, their authorisation, and their side effects.

// ── Create ───────────────────────────────────────────────────────────────────

func TestCreate_Validation(t *testing.T) {
	reporter := uuid.New()
	guest := "guest@example.com"
	empty := ""

	cases := []struct {
		name    string
		input   ticket.CreateInput
		wantErr bool
	}{
		{
			name:  "reporter user is sufficient",
			input: ticket.CreateInput{Subject: "Printer jammed", ReporterUserID: &reporter},
		},
		{
			name:  "guest email is sufficient",
			input: ticket.CreateInput{Subject: "Printer jammed", GuestEmail: &guest},
		},
		{
			name:    "subject is required",
			input:   ticket.CreateInput{Subject: "", ReporterUserID: &reporter},
			wantErr: true,
		},
		{
			name:    "whitespace-only subject is not a subject",
			input:   ticket.CreateInput{Subject: "   \t\n ", ReporterUserID: &reporter},
			wantErr: true,
		},
		{
			// Both columns feed tickets.search_vector; Postgres rejects an
			// oversized tsvector, so the cap is enforced before the write.
			name:    "subject over the cap is refused",
			input:   ticket.CreateInput{Subject: strings.Repeat("x", ticket.MaxSubjectLength+1), ReporterUserID: &reporter},
			wantErr: true,
		},
		{
			name: "description over the cap is refused",
			input: ticket.CreateInput{
				Subject:        "Fine subject",
				Description:    strings.Repeat("x", ticket.MaxDescriptionLength+1),
				ReporterUserID: &reporter,
			},
			wantErr: true,
		},
		{
			name:    "anonymous with no guest email is refused",
			input:   ticket.CreateInput{Subject: "Who are you"},
			wantErr: true,
		},
		{
			name:    "empty guest email does not count as a reporter",
			input:   ticket.CreateInput{Subject: "Who are you", GuestEmail: &empty},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			got, err := h.svc.Create(context.Background(), tc.input)

			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ticket.ErrValidation,
					"a rejected input must be distinguishable from an infrastructure failure")
				require.Equal(t, 0, h.store.creates, "a rejected ticket must not be written")
				return
			}

			require.NoError(t, err)
			require.Equal(t, h.newStatus.ID, got.StatusID, "a new ticket starts in New")
			require.NotEmpty(t, got.TrackingNumber)
			require.Equal(t, 1, h.store.creates)
		})
	}
}

// TestCreate_TrimsSubjectAndRecordsOpening pins the bookkeeping a new ticket
// must leave behind: the opening status-history entry, the audit entry, and
// the created event.
func TestCreate_TrimsSubjectAndRecordsOpening(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()

	got, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:        "  Padded subject  ",
		ReporterUserID: &reporter,
	})
	require.NoError(t, err)

	require.Equal(t, "Padded subject", got.Subject, "the subject is stored trimmed")
	require.Equal(t, 1, h.store.historyCreates, "opening a ticket records its first status")
	require.Len(t, h.auditStore.entries, 1, "opening a ticket is audited")
	require.Equal(t, "created", h.auditStore.entries[0].Action)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketCreated)
}

// TestCreate_SequenceFailureCreatesNothing covers the one infrastructure error
// on the path. A tracking number cannot be invented locally, so the ticket must
// not be written without one.
func TestCreate_SequenceFailureCreatesNothing(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	h.store.errNextSeq = errStoreDown

	_, err := h.svc.Create(context.Background(), ticket.CreateInput{
		Subject:        "Sequence is down",
		ReporterUserID: &reporter,
	})

	require.Error(t, err)
	require.ErrorIs(t, err, errStoreDown)
	require.Equal(t, 0, h.store.creates)
	require.Empty(t, h.dispatcher.events, "nothing may be announced for a ticket that was not created")
}

// ── Status transitions ───────────────────────────────────────────────────────

// TestUpdateStatus_Authorisation is the access-control table for the lifecycle.
// A regular user may never set a status directly, and only an admin may close.
func TestUpdateStatus_Authorisation(t *testing.T) {
	cases := []struct {
		name    string
		role    user.Role
		toName  string
		allowed bool
	}{
		{name: "staff may move to a normal status", role: user.RoleStaff, toName: ticket.StatusNameResolved, allowed: true},
		{name: "admin may move to a normal status", role: user.RoleAdmin, toName: ticket.StatusNameResolved, allowed: true},
		{name: "admin may close", role: user.RoleAdmin, toName: ticket.StatusNameClosed, allowed: true},
		{name: "staff may NOT close", role: user.RoleStaff, toName: ticket.StatusNameClosed, allowed: false},
		{name: "user may not set any status", role: user.RoleUser, toName: ticket.StatusNameResolved, allowed: false},
		{name: "user may not close", role: user.RoleUser, toName: ticket.StatusNameClosed, allowed: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedOpen()
			target := h.statusNamed(tc.toName)
			actorID := uuid.New()

			got, err := h.svc.UpdateStatus(context.Background(), seeded.ID, target.ID,
				ticket.Actor{UserID: &actorID, Role: tc.role})

			if !tc.allowed {
				require.Error(t, err)
				require.ErrorIs(t, err, ticket.ErrForbidden)
				require.Equal(t, 0, h.store.updates, "a refused transition must not write")
				require.Empty(t, h.dispatcher.events, "a refused transition must not notify")
				return
			}

			require.NoError(t, err)
			require.Equal(t, target.ID, got.StatusID)
			require.Equal(t, 1, h.store.historyCreates)
			require.Contains(t, h.dispatcher.types(), notification.EventTicketStatusChanged)
		})
	}
}

// TestUpdateStatus_UnknownStatusIsRefused guards against moving a ticket to a
// status that does not exist — which would otherwise persist a dangling ID.
func TestUpdateStatus_UnknownStatusIsRefused(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	actorID := uuid.New()

	_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, uuid.New(),
		ticket.Actor{UserID: &actorID, Role: user.RoleAdmin})

	require.Error(t, err)
	require.Equal(t, 0, h.store.updates)
}

// ── Resolve / Close / Reopen ─────────────────────────────────────────────────

func TestResolve(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()

	got, err := h.svc.Resolve(context.Background(), seeded.ID, "Replaced the toner",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Equal(t, h.resolvedStatus.ID, got.StatusID)
	require.NotNil(t, got.ResolvedAt, "resolving stamps the resolution time")
	require.NotNil(t, got.ResolutionNotes)
	require.Equal(t, "Replaced the toner", *got.ResolutionNotes)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketResolved)
}

func TestResolve_UserMayNotResolve(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	reporter := uuid.New()

	_, err := h.svc.Resolve(context.Background(), seeded.ID, "I fixed it myself",
		ticket.Actor{UserID: &reporter, Role: user.RoleUser})

	require.Error(t, err)
	require.ErrorIs(t, err, ticket.ErrForbidden)
	require.Equal(t, 0, h.store.updates)
}

// TestClose_BypassesTransitionRules pins documented behaviour that reads like a
// bug if you meet it cold: Close deliberately does NOT consult
// CanTransitionStatus, because the auto-close scheduler has no actor. The
// authorisation decision belongs to the caller.
func TestClose_BypassesTransitionRules(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))

	stored, err := h.store.GetByID(context.Background(), seeded.ID)
	require.NoError(t, err)
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
	require.NotNil(t, stored.ClosedAt)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketClosed)
}

func TestReopen(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedClosed()
	agent := uuid.New()

	got, err := h.svc.Reopen(context.Background(), seeded.ID, h.newStatus.ID,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Equal(t, h.newStatus.ID, got.StatusID)
	require.Nil(t, got.ClosedAt, "reopening clears the closed timestamp")
	require.Nil(t, got.ResolvedAt, "reopening clears the resolved timestamp")
	require.Contains(t, h.dispatcher.types(), notification.EventTicketReopened)
}

func TestReopen_UserMayNot(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedClosed()
	reporter := uuid.New()

	_, err := h.svc.Reopen(context.Background(), seeded.ID, h.newStatus.ID,
		ticket.Actor{UserID: &reporter, Role: user.RoleUser})

	require.ErrorIs(t, err, ticket.ErrForbidden)
	require.Equal(t, 0, h.store.updates)
}

// TestReopen_OnlyFromClosed keeps Reopen from being a general-purpose status
// setter that sidesteps the transition rules in UpdateStatus.
func TestReopen_OnlyFromClosed(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen() // New, not Closed
	agent := uuid.New()

	_, err := h.svc.Reopen(context.Background(), seeded.ID, h.newStatus.ID,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	require.Equal(t, 0, h.store.updates)
}

// ── Assignment ───────────────────────────────────────────────────────────────

func TestAssign(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	agent := uuid.New()
	assignee := uuid.New()

	got, err := h.svc.Assign(context.Background(), seeded.ID, &assignee, nil,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	require.NotNil(t, got.AssigneeUserID)
	require.Equal(t, assignee, *got.AssigneeUserID)
	require.Contains(t, h.dispatcher.types(), notification.EventTicketAssigned)
	require.Len(t, h.auditStore.entries, 1)
	require.Equal(t, "assigned", h.auditStore.entries[0].Action)
}

// TestAssign_ToNobodyIsUnassignment covers clearing an assignment, which shares
// the same path and would otherwise be untested.
func TestAssign_ToNobodyIsUnassignment(t *testing.T) {
	h := newHarness(t)
	previous := uuid.New()
	seeded := h.seedOpen()
	seeded.AssigneeUserID = &previous
	h.store.seed(seeded)
	agent := uuid.New()

	got, err := h.svc.Assign(context.Background(), seeded.ID, nil, nil,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Nil(t, got.AssigneeUserID, "assigning to nobody clears the assignee")
	require.Nil(t, got.AssigneeGroupID)
}

// ── Status administration ────────────────────────────────────────────────────

// TestRemoveStatus_ProtectsSystemStatuses stops an admin deleting New, Resolved
// or Closed. The service caches their IDs at startup, so losing one leaves
// every subsequent transition pointing at a row that is gone.
func TestRemoveStatus_ProtectsSystemStatuses(t *testing.T) {
	for _, name := range []string{ticket.StatusNameNew, ticket.StatusNameResolved, ticket.StatusNameClosed} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			err := h.svc.RemoveStatus(context.Background(), h.statusNamed(name).ID)
			require.Error(t, err, "system status %q must not be deletable", name)
			require.Contains(t, err.Error(), "system status")
		})
	}
}

// TestRemoveStatus_RefusesStatusInUse protects tickets from being orphaned on a
// status that no longer exists.
func TestRemoveStatus_RefusesStatusInUse(t *testing.T) {
	h := newHarness(t)
	custom := ticket.Status{ID: uuid.New(), Name: "Waiting on vendor", Kind: ticket.StatusKindCustom, Active: true}
	h.statuses.byName[custom.Name] = custom
	h.statuses.counts[custom.ID] = 3

	err := h.svc.RemoveStatus(context.Background(), custom.ID)

	require.Error(t, err)
	require.Contains(t, err.Error(), "deactivate")
	require.Equal(t, 0, h.statuses.deletes, "a status in use must not be deleted")
}

func TestRemoveStatus_DeletesUnusedCustomStatus(t *testing.T) {
	h := newHarness(t)
	custom := ticket.Status{ID: uuid.New(), Name: "Obsolete", Kind: ticket.StatusKindCustom, Active: true}
	h.statuses.byName[custom.Name] = custom

	require.NoError(t, h.svc.RemoveStatus(context.Background(), custom.ID))
	require.Equal(t, 1, h.statuses.deletes)
}

// ── Startup ──────────────────────────────────────────────────────────────────

// TestLoadSystemStatuses_FailsLoudlyWhenMissing matters because every other
// method dereferences s.sys. A missing system status must stop the process at
// startup rather than panic on the first ticket.
func TestLoadSystemStatuses_FailsLoudlyWhenMissing(t *testing.T) {
	for _, missing := range []string{ticket.StatusNameNew, ticket.StatusNameResolved, ticket.StatusNameClosed} {
		t.Run("missing "+missing, func(t *testing.T) {
			h := newHarness(t)
			delete(h.statuses.byName, missing)

			svc := ticket.NewService(h.store, h.statuses, h.dispatcher, h.auditStore, h.atomic, h.sla)
			err := svc.LoadSystemStatuses(context.Background())

			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), strings.ToLower(missing))
		})
	}
}

var _ = time.Now

// staffID is the acting agent for the lifecycle tests below.
var staffID = uuid.New()

// historyFor returns the status-history rows the fake store recorded for one
// ticket; the fake keeps a single flat slice across all tickets.
func historyFor(h *harness, ticketID uuid.UUID) []ticket.StatusHistoryEntry {
	var out []ticket.StatusHistoryEntry
	for _, e := range h.store.history {
		if e.TicketID == ticketID {
			out = append(out, e)
		}
	}
	return out
}

// UpdateStatus is a second door into Resolved and Closed, and it used to set
// StatusID alone. A ticket resolved through it had a NULL resolved_at, which
// CanUserUpdate reads as "resolved but no timestamp — permanently resolved",
// so the reporter was refused inside an open reopen window; it was also
// invisible to ListResolvedBefore and would never auto-close.
func TestUpdateStatus_MaintainsResolvedAndClosedTimestamps(t *testing.T) {
	t.Run("moving to Resolved stamps resolved_at", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedOpen()

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.ResolvedAt, "a ticket resolved this way must carry a timestamp")
		require.Nil(t, stored.ClosedAt)
	})

	t.Run("moving to Closed stamps closed_at", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedOpen()

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.closedStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleAdmin})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.ClosedAt)
	})

	// The mirror image, and the one with teeth: once the auto-close scheduler
	// is wired, a stale resolved_at on an actively-worked ticket means
	// ListResolvedBefore closes it underneath whoever is working it.
	t.Run("moving off Resolved clears resolved_at", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)
		require.NotNil(t, seeded.ResolvedAt, "precondition: the ticket is resolved")

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.newStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.ResolvedAt, "a reopened ticket must not look resolved to the scheduler")
		require.Nil(t, stored.ClosedAt)
	})

	// Re-resolving must not silently extend the reopen window.
	t.Run("re-resolving preserves the original timestamp", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)
		original := *seeded.ResolvedAt

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.ResolvedAt)
		require.True(t, stored.ResolvedAt.Equal(original),
			"re-resolving must not restart the reopen window")
	})
}

// Close is reached by the scheduler AND by an administrator pressing the
// button. It hardcoded SystemActor and wrote no audit entry, so a manual close
// showed as "System" in the timeline with nothing in the audit log, while
// DESIGN.md requires history to name whoever made the change.
func TestClose_AttributesAndAuditsTheActor(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	admin := uuid.New()

	require.NoError(t, h.svc.Close(context.Background(), seeded.ID,
		ticket.Actor{UserID: &admin, Role: user.RoleAdmin}))

	entries := historyFor(h, seeded.ID)
	require.NotEmpty(t, entries, "the close must be recorded in status history")
	last := entries[len(entries)-1]
	require.NotNil(t, last.ChangedByUserID, "a manual close must not be attributed to System")
	require.Equal(t, admin, *last.ChangedByUserID)

	require.NotEmpty(t, h.auditStore.entries, "closing must write an audit entry")
	require.Equal(t, "closed", h.auditStore.entries[len(h.auditStore.entries)-1].Action)
}

// The scheduler still has no actor, and must still be able to close.
func TestClose_SystemActorStillWorks(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))

	entries := historyFor(h, seeded.ID)
	require.NotEmpty(t, entries)
	require.Nil(t, entries[len(entries)-1].ChangedByUserID, "the scheduler has no user")
}

// Re-closing appended a duplicate Closed->Closed history row and re-fired the
// notification.
func TestClose_IsIdempotent(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))
	before := len(historyFor(h, seeded.ID))
	countClosed := func() int {
		n := 0
		for _, e := range h.dispatcher.types() {
			if e == notification.EventTicketClosed {
				n++
			}
		}
		return n
	}
	firedBefore := countClosed()

	require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))

	require.Len(t, historyFor(h, seeded.ID), before, "no duplicate history row")
	require.Equal(t, firedBefore, countClosed(), "no second close notification")
}

// An unresolvable configured reopen target arrived as uuid.Nil, reached the
// status_id foreign key mid-transaction, and took the user's reply with it —
// surfacing as a 500 on an ordinary reply.
func TestReopenPaths_RejectAnUnusableTargetBeforeWriting(t *testing.T) {
	t.Run("AddReply auto-reopen", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)

		_, err := h.svc.AddReply(context.Background(), seeded.ID,
			"It is broken again", false, true, "reporter@example.com",
			ticket.Actor{UserID: &reporter, Role: user.RoleUser},
			30, uuid.Nil)

		require.ErrorIs(t, err, ticket.ErrValidation)
		require.Empty(t, h.store.replies[seeded.ID], "the reply must not be written either")
	})

	t.Run("manual Reopen", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedOpen()
		require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))

		_, err := h.svc.Reopen(context.Background(), seeded.ID, uuid.Nil,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.ErrorIs(t, err, ticket.ErrValidation)
	})
}

// A custom status with zero CURRENT tickets can still be referenced by past
// transitions, and ticket_status_history has no ON DELETE action — so the
// DELETE failed on a foreign key and surfaced as a raw 500, contradicting the
// "deactivate instead of deleting" guidance, which implies a zero-count status
// is deletable.
func TestRemoveStatus_RefusesWhenHistoryReferencesIt(t *testing.T) {
	h := newHarness(t)
	custom := ticket.Status{ID: uuid.New(), Name: "In Progress", Kind: ticket.StatusKindCustom}
	h.statuses.byName[custom.Name] = custom

	// No current tickets, but one past transition through it.
	h.statuses.historyByStatus = map[uuid.UUID]int64{custom.ID: 3}

	err := h.svc.RemoveStatus(context.Background(), custom.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "past ticket transition",
		"the refusal must explain why, not fail on a foreign key")
	require.Contains(t, err.Error(), "deactivate")
	require.Zero(t, h.statuses.deletes, "nothing may be deleted")
}

func TestRemoveStatus_DeletesWhenNothingReferencesIt(t *testing.T) {
	h := newHarness(t)
	custom := ticket.Status{ID: uuid.New(), Name: "Awaiting Parts", Kind: ticket.StatusKindCustom}
	h.statuses.byName[custom.Name] = custom

	require.NoError(t, h.svc.RemoveStatus(context.Background(), custom.ID))
	require.Equal(t, 1, h.statuses.deletes)
}
