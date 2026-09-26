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
// ticket must not re-run the resolve side effects — no second status-history
// row, no second audit entry, no second guest-token rotation, no second
// EventTicketResolved dispatch. close() already has an alreadyClosed guard
// for exactly this class of problem; resolveInTx now has the equivalent,
// shared by both Resolve and ResolveAsDuplicate.
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
	require.Equal(t, baseHistory+1, firstHistoryLen, "exactly one status-history row for the resolve")
	require.Equal(t, baseAudit+1, firstAuditLen, "exactly one audit entry for the resolve")
	require.Equal(t, baseTokenCreates+1, firstTokenCreates, "exactly one guest-token rotation")
	require.Equal(t, 1, firstResolvedDispatches, "exactly one EventTicketResolved dispatch")

	// Double-submit: identical call again against the now-Resolved source.
	second, err := h.svc.ResolveAsDuplicate(context.Background(), source.ID, target.ID, "dup", staff)
	require.NoError(t, err)
	require.Equal(t, h.resolvedStatus.ID, second.StatusID)

	require.Equal(t, firstHistoryLen, len(h.store.history), "no second status-history row")
	require.Equal(t, firstAuditLen, len(h.auditStore.entries), "no second audit entry")
	require.Equal(t, firstTokenCreates, h.store.guestTokenCreates, "no second guest-token rotation")
	require.Equal(t, firstResolvedDispatches, countType(h.dispatcher.events, notification.EventTicketResolved),
		"no second EventTicketResolved dispatch")
}

// TestResolve_DoubleSubmitIsIdempotent is TestResolveAsDuplicate_DoubleSubmitIsIdempotent's
// twin for the plain Resolve path, since resolveInTx's guard is shared by
// both callers.
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

	_, err = h.svc.Resolve(context.Background(), source.ID, "done again", staff)
	require.NoError(t, err)

	require.Equal(t, firstHistoryLen, len(h.store.history), "no second status-history row")
	require.Equal(t, firstAuditLen, len(h.auditStore.entries), "no second audit entry")
	require.Equal(t, firstTokenCreates, h.store.guestTokenCreates, "no second guest-token rotation")
	require.Equal(t, firstResolvedDispatches, countType(h.dispatcher.events, notification.EventTicketResolved),
		"no second EventTicketResolved dispatch")
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
