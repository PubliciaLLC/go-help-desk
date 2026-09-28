package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// #129: a per-ticket activity feed, so "who changed this and when" is
// answerable without database access. ListAuditEntries is a thin filter over
// the shared audit store — this pins its two real responsibilities: scoping
// to one ticket, and reading oldest-first regardless of how the store itself
// orders things.
func TestListAuditEntries_ScopesToOneTicketAndReadsOldestFirst(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	ticketA := uuid.New()
	ticketB := uuid.New()
	base := time.Now().Add(-time.Hour)

	// Seeded out of chronological order and interleaved with a second
	// ticket's entries and an unrelated entity type, so passing this test
	// requires actually filtering and sorting rather than returning
	// whatever was appended last.
	h.auditStore.entries = []audit.Entry{
		{ID: uuid.New(), EntityType: "ticket", EntityID: ticketA, Action: "resolved", CreatedAt: base.Add(20 * time.Minute)},
		{ID: uuid.New(), EntityType: "ticket", EntityID: ticketB, Action: "created", CreatedAt: base.Add(5 * time.Minute)},
		{ID: uuid.New(), EntityType: "ticket", EntityID: ticketA, Action: "created", CreatedAt: base},
		{ID: uuid.New(), EntityType: "user", EntityID: ticketA, Action: "mfa_reset", CreatedAt: base.Add(10 * time.Minute)},
		{ID: uuid.New(), EntityType: "ticket", EntityID: ticketA, Action: "assigned", CreatedAt: base.Add(10 * time.Minute)},
	}

	got, err := h.svc.ListAuditEntries(ctx, ticketA)
	require.NoError(t, err)
	require.Len(t, got, 3, "ticket B's entry and the user-entity entry must not leak in")

	require.Equal(t, "created", got[0].Action)
	require.Equal(t, "assigned", got[1].Action)
	require.Equal(t, "resolved", got[2].Action)
}

// A ticket with nothing recorded against it yet (freshly seeded, no lifecycle
// events) gets an empty feed, not an error.
func TestListAuditEntries_NoEntriesIsEmptyNotAnError(t *testing.T) {
	h := newHarness(t)

	got, err := h.svc.ListAuditEntries(context.Background(), uuid.New())

	require.NoError(t, err)
	require.Empty(t, got)
}
