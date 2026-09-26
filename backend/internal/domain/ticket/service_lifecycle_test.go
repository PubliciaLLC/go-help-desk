package ticket_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

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
