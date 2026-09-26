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

// TestResolveAsDuplicate_DoubleSubmitIsIdempotent pins #209: a stale second
// tab re-submitting resolve_as_duplicate against an already-resolved source
// ticket, with IDENTICAL notes and the SAME target, must not re-run the
// resolve side effects — no second status-history row, no second audit
// entry, no second guest-token rotation, no second EventTicketResolved
// dispatch, and (#229) no second EventTicketLinked dispatch either, since no
// new link was created the second time. close() already has an
// alreadyClosed guard for exactly this class of problem; resolveInTx now has
// the equivalent, shared by both Resolve and ResolveAsDuplicate. See #225 for
// the narrower guard this pins alongside #209: a double-submit is only a
// true no-op when the notes AND the target match what's already stored,
// which they do here (see TestResolveAsDuplicate_DifferentNotesUpdateOnReResolve
// for the case where they don't).
func TestResolveAsDuplicate_DoubleSubmitIsIdempotent(t *testing.T) {
	h := newHarness(t)
	source, _ := guestTicket(t, h) // GuestEmail set, so resolving mints/rotates a token
	target := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	// Baseline before the first resolve: guestTicket's own Create already
	// wrote a New-status history row and an audit entry, so the counts to
	// compare against are deltas from here, not absolute counts.
	baseHistory := len(h.store.history)
	baseAudit := len(h.auditStore.entries)
	baseTokenCreates := h.store.guestTokenCreates

	first, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "dup", staff)
	require.NoError(t, err)
	require.Equal(t, h.resolvedStatus.ID, first.StatusID)

	firstHistoryLen := len(h.store.history)
	firstAuditLen := len(h.auditStore.entries)
	firstTokenCreates := h.store.guestTokenCreates
	firstResolvedDispatches := countType(h.dispatcher.events, notification.EventTicketResolved)
	firstLinkedDispatches := countType(h.dispatcher.events, notification.EventTicketLinked)
	require.Equal(t, baseHistory+1, firstHistoryLen, "exactly one status-history row for the resolve")
	require.Equal(t, baseAudit+1, firstAuditLen, "exactly one audit entry for the resolve")
	require.Equal(t, baseTokenCreates+1, firstTokenCreates, "exactly one guest-token rotation")
	require.Equal(t, 1, firstResolvedDispatches, "exactly one EventTicketResolved dispatch")
	require.Equal(t, 1, firstLinkedDispatches, "exactly one EventTicketLinked dispatch for the new link")

	// Double-submit: identical call again against the now-Resolved source.
	second, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "dup", staff)
	require.NoError(t, err)
	require.Equal(t, h.resolvedStatus.ID, second.StatusID)

	require.Equal(t, firstHistoryLen, len(h.store.history), "no second status-history row")
	require.Equal(t, firstAuditLen, len(h.auditStore.entries), "no second audit entry")
	require.Equal(t, firstTokenCreates, h.store.guestTokenCreates, "no second guest-token rotation")
	require.Equal(t, firstResolvedDispatches, countType(h.dispatcher.events, notification.EventTicketResolved),
		"no second EventTicketResolved dispatch")
	require.Equal(t, firstLinkedDispatches, countType(h.dispatcher.events, notification.EventTicketLinked),
		"no second EventTicketLinked dispatch (#229): the link already existed, so this call created nothing new to announce")
}

// TestResolveAsDuplicate_DifferentNotesUpdateOnReResolve pins #225's other
// half for the ResolveAsDuplicate door: the SAME target with DIFFERENT notes
// is a genuine re-resolve, not a no-op, and must update the notes.
func TestResolveAsDuplicate_DifferentNotesUpdateOnReResolve(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	first, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "first resolve", staff)
	require.NoError(t, err)
	require.Equal(t, "first resolve", *first.ResolutionNotes)
	require.NotNil(t, first.ResolvedAt)

	second, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "second resolve", staff)
	require.NoError(t, err)
	require.NotNil(t, second.ResolutionNotes)
	require.Equal(t, "second resolve", *second.ResolutionNotes,
		"different notes against an already-resolved ticket must update, not be silently dropped (#225)")
	require.NotNil(t, second.ResolvedAt)
	require.True(t, first.ResolvedAt.Equal(*second.ResolvedAt),
		"a re-resolve must preserve the ORIGINAL resolved_at, not restart the reopen window")

	// Same target both times, so no second link and no second EventTicketLinked
	// dispatch (#229) — only the genuine notes change drives the re-resolve.
	links, err := h.store.ListLinks(context.Background(), source.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, 1, countType(h.dispatcher.events, notification.EventTicketLinked))
	require.Equal(t, 2, countType(h.dispatcher.events, notification.EventTicketResolved),
		"a genuine re-resolve dispatches EventTicketResolved again, unlike a true double-submit")
}

// TestResolve_DoubleSubmitIsIdempotent is TestResolveAsDuplicate_DoubleSubmitIsIdempotent's
// twin for the plain Resolve path, since resolveInTx's guard is shared by
// both callers. This is the true double-submit case #209 was written for:
// IDENTICAL notes against an already-resolved ticket.
func TestResolve_DoubleSubmitIsIdempotent(t *testing.T) {
	h := newHarness(t)
	source, _ := guestTicket(t, h)
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	baseHistory := len(h.store.history)
	baseAudit := len(h.auditStore.entries)
	baseTokenCreates := h.store.guestTokenCreates

	_, err := h.svc.Resolve(context.Background(), source.ID, "done", staff)
	require.NoError(t, err)

	firstHistoryLen := len(h.store.history)
	firstAuditLen := len(h.auditStore.entries)
	firstTokenCreates := h.store.guestTokenCreates
	firstResolvedDispatches := countType(h.dispatcher.events, notification.EventTicketResolved)
	require.Equal(t, baseHistory+1, firstHistoryLen, "exactly one status-history row for the resolve")
	require.Equal(t, baseAudit+1, firstAuditLen, "exactly one audit entry for the resolve")
	require.Equal(t, baseTokenCreates+1, firstTokenCreates, "exactly one guest-token rotation")
	require.Equal(t, 1, firstResolvedDispatches, "exactly one EventTicketResolved dispatch")

	// True double-submit: IDENTICAL notes ("done" again) against the now-Resolved
	// ticket must be a full no-op.
	second, err := h.svc.Resolve(context.Background(), source.ID, "done", staff)
	require.NoError(t, err)
	require.Equal(t, "done", *second.ResolutionNotes)

	require.Equal(t, firstHistoryLen, len(h.store.history), "no second status-history row")
	require.Equal(t, firstAuditLen, len(h.auditStore.entries), "no second audit entry")
	require.Equal(t, firstTokenCreates, h.store.guestTokenCreates, "no second guest-token rotation")
	require.Equal(t, firstResolvedDispatches, countType(h.dispatcher.events, notification.EventTicketResolved),
		"no second EventTicketResolved dispatch")
}

// TestResolve_DifferentNotesUpdateOnReResolve pins #225: the old guard
// short-circuited on t.StatusID alone, so ANY resolve call on an
// already-Resolved ticket — including one with genuinely different notes —
// silently returned 200 with the STALE notes. Different notes must now
// actually update, while the original resolved_at (the reopen-window anchor)
// stays put, and the resolve side effects (history, audit, dispatch) run
// again since this is a real state change, not a repeat of the same request.
func TestResolve_DifferentNotesUpdateOnReResolve(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	agent := uuid.New()
	staff := ticket.Actor{UserID: &agent, Role: user.RoleStaff}

	baseHistory := len(h.store.history)
	baseAudit := len(h.auditStore.entries)

	first, err := h.svc.Resolve(context.Background(), source.ID, "done", staff)
	require.NoError(t, err)
	require.Equal(t, "done", *first.ResolutionNotes)
	require.NotNil(t, first.ResolvedAt)

	second, err := h.svc.Resolve(context.Background(), source.ID, "actually, done differently", staff)
	require.NoError(t, err)
	require.NotNil(t, second.ResolutionNotes)
	require.Equal(t, "actually, done differently", *second.ResolutionNotes,
		"a genuine re-resolve with different notes must update them, not silently drop them (#225)")
	require.NotNil(t, second.ResolvedAt)
	require.True(t, first.ResolvedAt.Equal(*second.ResolvedAt),
		"a re-resolve must preserve the ORIGINAL resolved_at (applyStatusTimestamps' rule), not restart the reopen window")

	require.Equal(t, baseHistory+2, len(h.store.history), "a genuine re-resolve records its own status-history row")
	require.Equal(t, baseAudit+2, len(h.auditStore.entries), "a genuine re-resolve records its own audit entry")
	require.Equal(t, 2, countType(h.dispatcher.events, notification.EventTicketResolved),
		"a genuine re-resolve dispatches EventTicketResolved again, unlike a true double-submit")
}

func countType(events []notification.Event, want notification.EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == want {
			n++
		}
	}
	return n
}
