package ticket_test

import (
	"context"
	"errors"
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

// Resolve and UpdateStatus are two doors into the same state, and they
// disagreed: UpdateStatus went through applyStatusTimestamps while Resolve set
// ResolvedAt by hand. So the rule the helper exists to enforce held on one path
// and not the other.
func TestResolve_UsesTheSameTimestampRuleAsUpdateStatus(t *testing.T) {
	// Re-resolving must not restart the reopen window. UpdateStatus already
	// guaranteed this; Resolve did not, so a reporter's window silently moved
	// depending on which endpoint staff happened to use.
	t.Run("re-resolving preserves the original timestamp", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)
		original := *seeded.ResolvedAt

		_, err := h.svc.Resolve(context.Background(), seeded.ID, "again",
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.True(t, stored.ResolvedAt.Equal(original),
			"resolving an already-resolved ticket must not extend the reopen window")
	})

	// Resolving a CLOSED ticket left closed_at set on a now-open ticket, which
	// hides it from the auto-close query permanently.
	t.Run("resolving a closed ticket clears closed_at", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedOpen()
		require.NoError(t, h.svc.Close(context.Background(), seeded.ID, ticket.SystemActor))

		closed, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, closed.ClosedAt, "precondition")

		_, err = h.svc.Resolve(context.Background(), seeded.ID, "reopened and resolved",
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.ClosedAt, "a resolved ticket is not closed")
		require.NotNil(t, stored.ResolvedAt)
	})

	// A ticket arriving from Closed is being resolved afresh, so it gets a
	// fresh stamp. Preserving the old one would judge the reporter against a
	// window that expired before this resolution happened.
	//
	// seedClosed carries a ResolvedAt from before the close, which is what
	// makes this meaningful: the preserve branch has to be rejected on the
	// strength of the OLD STATUS, not because the field happened to be nil.
	//
	// An earlier version of this subtest drove UpdateStatus despite its name,
	// so the Resolve call site was never exercised and passing it the wrong
	// old status left the suite green. Its fixture was fine — the ticket did
	// carry a stale ResolvedAt, because Close sets ClosedAt without clearing
	// it. Only the door was wrong.
	t.Run("Resolve on a closed ticket gets a fresh timestamp", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		stale := *seeded.ResolvedAt

		_, err := h.svc.Resolve(context.Background(), seeded.ID, "resolved again",
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.True(t, stored.ResolvedAt.After(stale),
			"a fresh resolution must not inherit the timestamp from before it was closed")
		require.Nil(t, stored.ClosedAt)
	})

	// The same rule through the other door, so neither call site can be given
	// the wrong old status without a test noticing.
	t.Run("UpdateStatus on a closed ticket gets a fresh timestamp", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedClosed()
		stale := *seeded.ResolvedAt

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.True(t, stored.ResolvedAt.After(stale))
		require.Nil(t, stored.ClosedAt)
	})
}

// SLA resolution was recorded in Resolve and not in UpdateStatus — the other
// door into Resolved. A ticket resolved by PATCHing status_id therefore left
// sla_records.resolved_at NULL, which the breach evaluator reads as "never
// resolved" and reports as a permanent false breach.
func TestUpdateStatus_RecordsTheSLAResolution(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.resolvedStatus.ID,
		ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Equal(t, 1, h.sla.resolutions,
		"resolving through UpdateStatus must record the SLA resolution too")
}

func TestResolve_RecordsTheSLAResolution(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	_, err := h.svc.Resolve(context.Background(), seeded.ID, "done",
		ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
	require.NoError(t, err)

	require.Equal(t, 1, h.sla.resolutions)
}

// Moving to any other status is not a resolution and must not record one.
func TestUpdateStatus_DoesNotRecordSLAForOtherStatuses(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()

	_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.closedStatus.ID,
		ticket.Actor{UserID: &staffID, Role: user.RoleAdmin})
	require.NoError(t, err)

	require.Zero(t, h.sla.resolutions)
}

// Every lifecycle write must read the row it is about to overwrite from inside
// its transaction. Update rewrites every column, so a copy read on the pool is
// a lost update waiting for a second writer.
//
// fakeStore counts the locking reads but nothing asserted the count, so
// reverting any one method to s.store.GetByID failed no test — the counter was
// decoration. This asserts it per method.
func TestLifecycleWrites_ReadUnderTheLock(t *testing.T) {
	cases := []struct {
		name string
		call func(*harness, ticket.Ticket) error
	}{
		{"UpdateStatus", func(h *harness, seeded ticket.Ticket) error {
			_, err := h.svc.UpdateStatus(context.Background(), seeded.ID,
				h.resolvedStatus.ID, ticket.Actor{Role: user.RoleStaff})
			return err
		}},
		{"Assign", func(h *harness, seeded ticket.Ticket) error {
			assignee := uuid.New()
			_, err := h.svc.Assign(context.Background(), seeded.ID, &assignee, nil,
				ticket.Actor{Role: user.RoleStaff})
			return err
		}},
		{"Resolve", func(h *harness, seeded ticket.Ticket) error {
			_, err := h.svc.Resolve(context.Background(), seeded.ID, "done",
				ticket.Actor{Role: user.RoleStaff})
			return err
		}},
		{"Close", func(h *harness, seeded ticket.Ticket) error {
			return h.svc.Close(context.Background(), seeded.ID,
				ticket.Actor{Role: user.RoleAdmin})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedOpen()
			before := h.store.forUpdateReads

			require.NoError(t, tc.call(h, seeded))

			require.Greater(t, h.store.forUpdateReads, before,
				"%s must read the ticket through GetByIDForUpdate inside its "+
					"transaction; reading on the pool loses concurrent writes", tc.name)
		})
	}
}

// ── SLA pause (#181) ─────────────────────────────────────────────────────────
//
// A target is measured against elapsed time since creation, MINUS time spent
// Pending. applyStatusTimestamps is the one place that opens and closes a
// pause interval, and every status door routes through it — these tests pin
// that routing for each door, plus the edge cases DESIGN.md calls out:
// multiple pause intervals, resolving while paused, and a reopen carrying the
// accumulated pause forward rather than resetting the clock.

// slaSeed plants a ticket in an arbitrary status with SLA pause fields set
// directly, bypassing the status doors — these tests are about what the doors
// do to a ticket that already carries pause state, not about how it got
// there.
func (h *harness) slaSeed(statusID uuid.UUID, pendingSince *time.Time, pausedSeconds int64) ticket.Ticket {
	reporter := uuid.New()
	now := time.Now()
	t := ticket.Ticket{
		ID:               uuid.New(),
		TrackingNumber:   ticket.TrackingNumber("HD-SLA-" + uuid.NewString()[:8]),
		Subject:          "SLA pause fixture",
		ReporterUserID:   &reporter,
		StatusID:         statusID,
		CreatedAt:        now.Add(-24 * time.Hour),
		UpdatedAt:        now.Add(-24 * time.Hour),
		PendingSince:     pendingSince,
		SLAPausedSeconds: pausedSeconds,
	}
	h.store.seed(t)
	return t
}

func TestStatusTransitions_MaintainSLAPause(t *testing.T) {
	t.Run("New to Pending opens an interval", func(t *testing.T) {
		h := newHarness(t)
		seeded := h.seedOpen()

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.pendingStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.PendingSince, "entering Pending must open an interval")
		require.Zero(t, stored.SLAPausedSeconds, "nothing has closed yet")
	})

	t.Run("Pending to In Progress closes the interval", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-90 * time.Second)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 0)

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.inProgressStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.PendingSince, "leaving Pending must close the interval")
		require.GreaterOrEqual(t, stored.SLAPausedSeconds, int64(90),
			"the closed interval's length must be added to the accumulated total")
	})

	t.Run("Pending to Pending leaves the interval start untouched", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-5 * time.Minute)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 0)

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.pendingStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.PendingSince)
		require.True(t, stored.PendingSince.Equal(pendingSince),
			"re-entering the same status must not restart the interval")
	})

	t.Run("Pending to Resolved via Resolve closes the interval and records the SLA resolution", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-120 * time.Second)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 0)

		_, err := h.svc.Resolve(context.Background(), seeded.ID, "fixed it",
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.PendingSince, "resolving must close the open interval")
		require.GreaterOrEqual(t, stored.SLAPausedSeconds, int64(120))
		require.Equal(t, 1, h.sla.resolutions)
	})

	t.Run("Pending to Closed via Close closes the interval", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-60 * time.Second)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 0)
		admin := uuid.New()

		require.NoError(t, h.svc.Close(context.Background(), seeded.ID,
			ticket.Actor{UserID: &admin, Role: user.RoleAdmin}))

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.PendingSince,
			"Close must route through the shared rule so a Pending ticket's interval closes")
		require.GreaterOrEqual(t, stored.SLAPausedSeconds, int64(60))
		require.NotNil(t, stored.ClosedAt)
	})

	t.Run("Closed to Pending via Reopen carries the accumulated pause forward", func(t *testing.T) {
		h := newHarness(t)
		closedAt := time.Now().Add(-time.Hour)
		reporter := uuid.New()
		seeded := ticket.Ticket{
			ID:               uuid.New(),
			TrackingNumber:   "HD-SLA-REOPEN",
			Subject:          "Reopen carries pause",
			ReporterUserID:   &reporter,
			StatusID:         h.closedStatus.ID,
			ResolvedAt:       &closedAt,
			ClosedAt:         &closedAt,
			CreatedAt:        time.Now().Add(-4 * time.Hour),
			UpdatedAt:        closedAt,
			SLAPausedSeconds: 300,
		}
		h.store.seed(seeded)
		agent := uuid.New()

		got, err := h.svc.Reopen(context.Background(), seeded.ID, h.pendingStatus.ID,
			ticket.Actor{UserID: &agent, Role: user.RoleStaff})
		require.NoError(t, err)

		require.NotNil(t, got.PendingSince, "reopening into Pending opens a fresh interval")
		require.Equal(t, int64(300), got.SLAPausedSeconds,
			"a reopen is not a new SLA clock: the accumulated pause must carry over unchanged")
	})

	t.Run("Resolved to Pending via a user reply auto-reopen opens an interval", func(t *testing.T) {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)

		_, err := h.svc.AddReply(context.Background(), seeded.ID,
			"Still broken, please hold", false, true, "reporter@example.com",
			ticket.Actor{UserID: &reporter, Role: user.RoleUser},
			7, h.pendingStatus.ID)
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.PendingSince,
			"auto-reopening into a Pending target must open an interval")
	})

	t.Run("two pause intervals accumulate", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-30 * time.Second)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 60)

		_, err := h.svc.UpdateStatus(context.Background(), seeded.ID, h.newStatus.ID,
			ticket.Actor{UserID: &staffID, Role: user.RoleStaff})
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.Nil(t, stored.PendingSince)
		require.GreaterOrEqual(t, stored.SLAPausedSeconds, int64(90),
			"the pre-existing 60s interval plus the newly-closed ~30s interval")
	})

	t.Run("a staff reply while Pending does not touch the pause", func(t *testing.T) {
		h := newHarness(t)
		pendingSince := time.Now().Add(-10 * time.Minute)
		seeded := h.slaSeed(h.pendingStatus.ID, &pendingSince, 0)
		agent := uuid.New()

		_, err := h.svc.AddReply(context.Background(), seeded.ID,
			"Looking into it", false, true, "reporter@example.com",
			ticket.Actor{UserID: &agent, Role: user.RoleStaff},
			7, h.newStatus.ID)
		require.NoError(t, err)

		stored, err := h.store.GetByID(context.Background(), seeded.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.PendingSince, "a reply is not a status change")
		require.True(t, stored.PendingSince.Equal(pendingSince),
			"the open interval must be untouched by a reply")
		require.Equal(t, 1, h.sla.firstResponses)
	})
}

// TestAutoClose_ResolvedPastWindowCloses verifies that a resolved ticket past
// its reopen window is closed by AutoClose.
func TestAutoClose_ResolvedPastWindowCloses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a ticket resolved 10 days ago.
	resolvedAt := time.Now().Add(-10 * 24 * time.Hour)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-15 * 24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// AutoClose with 7-day window; ticket is past it.
	closed, err := h.svc.AutoClose(ctx, 7, 100)

	require.NoError(t, err)
	require.Equal(t, 1, closed)

	// Verify the ticket is now Closed.
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
	require.NotNil(t, stored.ClosedAt)

	// Verify a status history entry was written.
	history, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, *history[0].FromStatusID, h.resolvedStatus.ID)
	require.Equal(t, history[0].ToStatusID, h.closedStatus.ID)
	require.Nil(t, history[0].ChangedByUserID)
	require.Equal(t, "", history[0].ChangedByName) // System actor → empty name

	// Verify EventTicketClosed was dispatched.
	require.Len(t, h.dispatcher.events, 1)
	require.Equal(t, h.dispatcher.events[0].Type, notification.EventTicketClosed)
	require.Nil(t, h.dispatcher.events[0].ActorID)
}

// TestAutoClose_ResolvedWithinWindowStaysResolved verifies that a resolved
// ticket within its window is not closed.
func TestAutoClose_ResolvedWithinWindowStaysResolved(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a ticket resolved 2 days ago.
	resolvedAt := time.Now().Add(-2 * 24 * time.Hour)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-5 * 24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// AutoClose with 7-day window; ticket is within it.
	closed, err := h.svc.AutoClose(ctx, 7, 100)

	require.NoError(t, err)
	require.Equal(t, 0, closed)

	// Verify the ticket is still Resolved.
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.resolvedStatus.ID, stored.StatusID)
	require.Nil(t, stored.ClosedAt)

	// No history or events.
	history, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Len(t, history, 0)
	require.Len(t, h.dispatcher.events, 0)
}

// TestAutoClose_WindowOfZeroClosesOnFirstSweep verifies that a window of 0
// days causes a ticket to be eligible on the very next sweep.
func TestAutoClose_WindowOfZeroClosesOnFirstSweep(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a ticket resolved 1 second ago.
	resolvedAt := time.Now().Add(-1 * time.Second)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// AutoClose with 0-day window.
	closed, err := h.svc.AutoClose(ctx, 0, 100)

	require.NoError(t, err)
	require.Equal(t, 1, closed)

	// Verify the ticket is Closed.
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
	require.NotNil(t, stored.ClosedAt)
}

// TestAutoClose_NegativeWindowBehavesAsZero verifies that a negative window
// also causes immediate eligibility (cutoff becomes future-dated).
func TestAutoClose_NegativeWindowBehavesAsZero(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a ticket resolved 1 second ago.
	resolvedAt := time.Now().Add(-1 * time.Second)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// AutoClose with -3-day window.
	closed, err := h.svc.AutoClose(ctx, -3, 100)

	require.NoError(t, err)
	require.Equal(t, 1, closed)

	// Verify the ticket is Closed.
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
	require.NotNil(t, stored.ClosedAt)
}

// TestAutoClose_ResolvedWithNoTimestampIsInvisible verifies that a resolved
// ticket with nil ResolvedAt is not listed and not closed.
func TestAutoClose_ResolvedWithNoTimestampIsInvisible(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a ticket in Resolved status but with nil ResolvedAt.
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     nil,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      time.Now(),
	}
	h.store.seed(tt)

	// AutoClose with 0-day window.
	closed, err := h.svc.AutoClose(ctx, 0, 100)

	require.NoError(t, err)
	require.Equal(t, 0, closed)

	// Verify the ticket is still Resolved.
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.resolvedStatus.ID, stored.StatusID)
	require.Nil(t, stored.ClosedAt)
}

// TestAutoClose_AttributedToSystem verifies that the close is attributed to
// "System" (nil user) in the status history.
func TestAutoClose_AttributedToSystem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	resolvedAt := time.Now().Add(-10 * 24 * time.Hour)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-15 * 24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	closed, err := h.svc.AutoClose(ctx, 7, 100)

	require.NoError(t, err)
	require.Equal(t, 1, closed)

	history, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)

	// System attribution: nil user.
	require.Nil(t, history[0].ChangedByUserID)
	require.Equal(t, "", history[0].ChangedByName)

	// Audit entry also has nil user.
	require.Len(t, h.auditStore.entries, 1)
	require.Nil(t, h.auditStore.entries[0].ActorID)
	require.Equal(t, h.auditStore.entries[0].Action, "closed")
}

// TestAutoClose_SecondSweepLeavesClosedTicketAlone verifies that running
// AutoClose twice does not re-close an already-closed ticket.
func TestAutoClose_SecondSweepLeavesClosedTicketAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	resolvedAt := time.Now().Add(-1 * time.Second)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// First sweep.
	closed1, err := h.svc.AutoClose(ctx, 0, 100)
	require.NoError(t, err)
	require.Equal(t, 1, closed1)

	// Record state after first sweep.
	historyAfterFirst, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	firstHistoryLen := len(historyAfterFirst)
	firstEventCount := len(h.dispatcher.events)
	firstUpdateCount := h.store.updates

	// Second sweep.
	closed2, err := h.svc.AutoClose(ctx, 0, 100)
	require.NoError(t, err)
	require.Equal(t, 0, closed2)

	// State unchanged.
	historyAfterSecond, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Equal(t, firstHistoryLen, len(historyAfterSecond))
	require.Equal(t, firstEventCount, len(h.dispatcher.events))
	require.Equal(t, firstUpdateCount, h.store.updates)
}

// TestAutoClose_SkipsTicketReopenedAfterListing verifies the race-condition
// guard: if a ticket is reopened between the list query and the close, it is
// skipped without any write.
func TestAutoClose_SkipsTicketReopenedAfterListing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	resolvedAt := time.Now().Add(-10 * 24 * time.Hour)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-15 * 24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// Simulate reopen happening after listing but before the lock is taken:
	// the read under FOR UPDATE returns the ticket with Resolved status changed
	// to New status and ResolvedAt cleared.
	h.store.onRead = func(t ticket.Ticket) ticket.Ticket {
		if t.ID == tt.ID {
			t.StatusID = h.newStatus.ID
			t.ResolvedAt = nil
		}
		return t
	}

	closed, err := h.svc.AutoClose(ctx, 7, 100)

	require.NoError(t, err)
	require.Equal(t, 0, closed)

	// No history or events.
	history, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Len(t, history, 0)
	require.Len(t, h.dispatcher.events, 0)

	// Verify no update call was made (the stored ticket data didn't change).
	require.Equal(t, 0, h.store.updates)
	// The in-memory ticket still has its original state (onRead didn't persist).
	require.Equal(t, h.resolvedStatus.ID, h.store.tickets[tt.ID].StatusID)
}

// TestAutoClose_ErrorsAreAccumulated verifies that multiple errors in a batch
// are joined and returned together without stopping the entire batch.
func TestAutoClose_ErrorsAreAccumulated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a single eligible ticket.
	resolvedAt := time.Now().Add(-10 * 24 * time.Hour)
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.resolvedStatus.ID,
		ResolvedAt:     &resolvedAt,
		CreatedAt:      time.Now().Add(-15 * 24 * time.Hour),
		UpdatedAt:      resolvedAt,
	}
	h.store.seed(tt)

	// Simulate a transient error when getting the ticket under lock.
	h.store.errGetByID = errors.New("connection error")

	closed, err := h.svc.AutoClose(ctx, 7, 100)

	require.Error(t, err)
	require.Equal(t, 0, closed)
	require.Contains(t, err.Error(), "HD-000001")
	require.Contains(t, err.Error(), "connection error")

	// Ticket should still be Resolved (no write occurred).
	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.resolvedStatus.ID, stored.StatusID)
	require.Nil(t, stored.ClosedAt)
}

// TestAutoClose_HonoursLimit verifies that AutoClose respects the limit
// parameter and returns exactly that many closed tickets.
func TestAutoClose_HonoursLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed three eligible tickets.
	resolvedAt := time.Now().Add(-10 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		tt := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.TrackingNumber("HD-00000" + string('1'+rune(i))),
			Subject:        "Issue",
			StatusID:       h.resolvedStatus.ID,
			ResolvedAt:     &resolvedAt,
			CreatedAt:      time.Now().Add(-15 * 24 * time.Hour),
			UpdatedAt:      resolvedAt,
		}
		h.store.seed(tt)
	}

	// Close with limit 2.
	closed, err := h.svc.AutoClose(ctx, 7, 2)

	require.NoError(t, err)
	require.Equal(t, 2, closed)

	// Count closed tickets.
	closedCount := 0
	for _, t := range h.store.tickets {
		if t.StatusID == h.closedStatus.ID {
			closedCount++
		}
	}
	require.Equal(t, 2, closedCount)

	// A second call should close the third.
	closed2, err := h.svc.AutoClose(ctx, 7, 2)
	require.NoError(t, err)
	require.Equal(t, 1, closed2)
}

// TestClose_BypassesTransitionRules pins the recorded decision: Close does not
// consult CanTransitionStatus, so admins can force-close from any status and
// the auto-close scheduler can close from Resolved.
func TestClose_BypassesTransitionRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a New ticket (not normally closeable).
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.newStatus.ID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	h.store.seed(tt)

	// Close should succeed even though the transition rule would forbid it.
	actor := ticket.Actor{UserID: nil, Role: ticket.SystemActor.Role}
	err := h.svc.Close(ctx, tt.ID, actor)

	require.NoError(t, err)

	stored := h.store.tickets[tt.ID]
	require.Equal(t, h.closedStatus.ID, stored.StatusID)
}

// TestClose_IsIdempotent pins that closing an already-closed ticket is a no-op.
func TestClose_IsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Seed a Closed ticket.
	now := time.Now()
	tt := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-000001",
		Subject:        "Printer offline",
		StatusID:       h.closedStatus.ID,
		ClosedAt:       &now,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      now,
	}
	h.store.seed(tt)

	// Close again.
	actor := ticket.Actor{UserID: nil, Role: ticket.SystemActor.Role}
	err := h.svc.Close(ctx, tt.ID, actor)

	require.NoError(t, err)

	// No new history or events.
	history, err := h.store.ListStatusHistory(ctx, tt.ID)
	require.NoError(t, err)
	require.Len(t, history, 0)
	require.Len(t, h.dispatcher.events, 0)

	// No update.
	require.Equal(t, 0, h.store.updates)
}
