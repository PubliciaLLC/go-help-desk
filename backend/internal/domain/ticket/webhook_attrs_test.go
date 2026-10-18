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

// seedClassified plants a ticket with a category, a critical priority and an
// assignee group, so the attrs a webhook filter reads are not zero values (#413).
func (h *harness) seedClassified(status uuid.UUID) ticket.Ticket {
	grp := uuid.New()
	reporter := uuid.New()
	t := ticket.Ticket{
		ID:              uuid.New(),
		TrackingNumber:  "HD-000200",
		Subject:         "Classified",
		CategoryID:      uuid.New(),
		Priority:        ticket.PriorityCritical,
		StatusID:        status,
		AssigneeGroupID: &grp,
		ReporterUserID:  &reporter,
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		UpdatedAt:       time.Now().Add(-2 * time.Hour),
	}
	h.store.seed(t)
	return t
}

// classification is the part of a ticket a webhook filter tests. The event now
// carries the whole ticket (#506); the filter reads these three.
type classification struct {
	CategoryID      uuid.UUID
	Priority        string
	AssigneeGroupID *uuid.UUID
}

// classificationOf is what a webhook filter must see for tk.
func classificationOf(tk ticket.Ticket) classification {
	return classification{
		CategoryID:      tk.CategoryID,
		Priority:        string(tk.Priority),
		AssigneeGroupID: tk.AssigneeGroupID,
	}
}

// requireAttrsOnEveryTicketEvent checks every ticket.* event the harness
// recorded, not one per type, so a new door is held to it the moment it fires
// (#448 adds ticket.note_added). It also insists that at least one event was
// recorded, or the check would pass on a call that announced nothing.
func requireAttrsOnEveryTicketEvent(t *testing.T, h *harness, want ticket.Ticket) {
	t.Helper()
	seen := 0
	for _, e := range h.dispatcher.events {
		if !strings.HasPrefix(string(e.Type), "ticket.") {
			continue
		}
		seen++
		require.NotNil(t, e.Ticket, "%s carries no ticket state", e.Type)
		require.Equal(t, classificationOf(want),
			classification{e.Ticket.CategoryID, e.Ticket.Priority, e.Ticket.AssigneeGroupID},
			"%s carries the wrong ticket state", e.Type)
	}
	require.NotZero(t, seen, "the call dispatched no ticket event; the check would pass vacuously")
}

// TestWebhookEvents_CarryTheTicketAttrs: every webhook event a ticket raises
// carries the ticket's classification, whichever door raised it. The follow-up
// case, which raises events about two tickets, is in its own test below.
func TestWebhookEvents_CarryTheTicketAttrs(t *testing.T) {
	ctx := context.Background()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}
	admin := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleAdmin}

	cases := []struct {
		name string
		run  func(t *testing.T, h *harness) ticket.Ticket // returns the ticket the events describe
	}{
		{"create", func(t *testing.T, h *harness) ticket.Ticket {
			got, err := h.svc.Create(ctx, ticket.CreateInput{
				Subject: "s", Description: "d", CategoryID: uuid.New(),
				Priority: ticket.PriorityHigh, ReporterUserID: ptr(uuid.New()),
			})
			require.NoError(t, err)
			return got
		}},
		{"status change", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			_, err := h.svc.UpdateStatus(ctx, seeded.ID, h.inProgressStatus.ID, staff)
			require.NoError(t, err)
			return seeded
		}},
		{"assign, the new group is in the event", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			newGrp := uuid.New()
			_, err := h.svc.Assign(ctx, seeded.ID, nil, &newGrp, staff)
			require.NoError(t, err)
			want := seeded
			want.AssigneeGroupID = &newGrp
			return want
		}},
		{"public reply", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			_, err := h.svc.AddReply(ctx, seeded.ID, "r", false, false, "x@example.com", staff, 30, h.newStatus.ID)
			require.NoError(t, err)
			return seeded
		}},
		{"internal note", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			_, err := h.svc.AddReply(ctx, seeded.ID, "r", true, false, "x@example.com", staff, 30, h.newStatus.ID)
			require.NoError(t, err)
			return seeded
		}},
		{"reply that reopens", func(t *testing.T, h *harness) ticket.Ticket {
			reporter := uuid.New()
			resolvedAt := time.Now().Add(-time.Hour)
			seeded := h.seedClassified(h.resolvedStatus.ID)
			seeded.ResolvedAt = &resolvedAt
			seeded.ReporterUserID = &reporter
			h.store.seed(seeded)
			requester := ticket.Actor{UserID: &reporter, Role: user.RoleUser}
			_, err := h.svc.AddReply(ctx, seeded.ID, "again", false, false, "x@example.com", requester, 30, h.newStatus.ID)
			require.NoError(t, err)
			require.Contains(t, h.dispatcher.types(), notification.EventTicketReopened,
				"the case must reopen, or it tests nothing about reopen")
			return seeded
		}},
		{"resolve", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			_, err := h.svc.Resolve(ctx, seeded.ID, "fixed", staff)
			require.NoError(t, err)
			return seeded
		}},
		{"resolve as duplicate (ticket.linked and ticket.resolved both describe the source)", func(t *testing.T, h *harness) ticket.Ticket {
			src := h.seedClassified(h.newStatus.ID)
			other := h.seedOpen()
			_, err := h.svc.ResolveAsDuplicate(ctx, src.ID, other.ID, "dup", staff)
			require.NoError(t, err)
			return src
		}},
		{"close", func(t *testing.T, h *harness) ticket.Ticket {
			seeded := h.seedClassified(h.newStatus.ID)
			require.NoError(t, h.svc.Close(ctx, seeded.ID, admin))
			return seeded
		}},
		{"force reopen", func(t *testing.T, h *harness) ticket.Ticket {
			h.policy = ticket.ReopenPolicyStaffAdmin
			closedAt := time.Now().Add(-time.Hour)
			seeded := h.seedClassified(h.closedStatus.ID)
			seeded.ResolvedAt = &closedAt
			seeded.ClosedAt = &closedAt
			h.store.seed(seeded)
			_, err := h.svc.Reopen(ctx, seeded.ID, h.newStatus.ID, staff)
			require.NoError(t, err)
			return seeded
		}},
		{"add link", func(t *testing.T, h *harness) ticket.Ticket {
			src := h.seedClassified(h.newStatus.ID)
			tgt := h.seedOpen()
			require.NoError(t, h.svc.AddLink(ctx, src.ID, tgt.ID, ticket.LinkRelatedTo, staff))
			return src
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			want := tc.run(t, h)
			requireAttrsOnEveryTicketEvent(t, h, want)
		})
	}
}

// TestWebhookEvents_FollowUpCarriesEachTicketsOwnAttrs: a follow-up raises
// ticket.linked about the original and ticket.created about the new ticket, and
// each event carries the classification of the ticket it names.
func TestWebhookEvents_FollowUpCarriesEachTicketsOwnAttrs(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	// A follow-up does not inherit the assignee group, so giving the original
	// one makes the two tickets' attrs differ: an event that described the
	// wrong ticket of the pair would then show.
	grp := uuid.New()
	orig.AssigneeGroupID = &grp
	h.store.seed(orig)
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	got, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", staff)
	require.NoError(t, err)
	require.NotEqual(t, classificationOf(orig), classificationOf(got),
		"the two tickets must differ, or the test cannot tell their events apart")

	var sawLinked, sawCreated bool
	for _, e := range h.dispatcher.events {
		switch e.Type {
		case notification.EventTicketLinked:
			sawLinked = true
			require.Equal(t, orig.ID, e.TicketID)
			require.NotNil(t, e.Ticket, "ticket.linked carries no ticket state")
			require.Equal(t, classificationOf(orig), classification{e.Ticket.CategoryID, e.Ticket.Priority, e.Ticket.AssigneeGroupID})
		case notification.EventTicketCreated:
			sawCreated = true
			require.Equal(t, got.ID, e.TicketID)
			require.NotNil(t, e.Ticket, "ticket.created carries no ticket state")
			require.Equal(t, classificationOf(got), classification{e.Ticket.CategoryID, e.Ticket.Priority, e.Ticket.AssigneeGroupID})
		}
	}
	require.True(t, sawLinked, "no ticket.linked event was raised")
	require.True(t, sawCreated, "no ticket.created event was raised")
}
