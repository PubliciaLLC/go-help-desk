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

// Replies and internal notes are audited (CTO ruling, 2026-10-20): one entry
// each, with the author as actor and no body text.

func replyEntries(h *harness, ticketID uuid.UUID) []audit.Entry {
	var out []audit.Entry
	for _, e := range h.auditStore.entries {
		if e.EntityID == ticketID && (e.Action == ticket.AuditActionReplyAdded || e.Action == ticket.AuditActionNoteAdded) {
			out = append(out, e)
		}
	}
	return out
}

func TestAddReply_WritesOneAuditEntryPerReplyAndNote(t *testing.T) {
	tests := []struct {
		name     string
		internal bool
		action   string
	}{
		{"public reply", false, "reply_added"},
		{"internal note", true, "note_added"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.seedOpen()
			agent := uuid.New()

			reply, err := h.svc.AddReply(context.Background(), tk.ID, "SECRET-BODY-TEXT", tc.internal, false, "",
				ticket.Actor{UserID: &agent, Role: user.RoleStaff}, 7, h.newStatus.ID)
			require.NoError(t, err)

			got := replyEntries(h, tk.ID)
			require.Len(t, got, 1)
			e := got[0]
			require.Equal(t, tc.action, e.Action)
			require.Equal(t, "ticket", e.EntityType)
			require.NotNil(t, e.ActorID)
			require.Equal(t, agent, *e.ActorID)
			require.Nil(t, e.Before)
			require.Equal(t, map[string]any{"reply_id": reply.ID, "internal": tc.internal}, e.After)
			require.NotContains(t, e.After, "body")
			require.NotContains(t, e.Before, "body")
		})
	}
}

// An audit entry committed apart from the reply it describes is not a trail.
func TestAddReply_AuditFailureRollsBackTheReply(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	agent := uuid.New()
	h.auditStore.failOnCall = 1

	_, err := h.svc.AddReply(context.Background(), tk.ID, "hello", false, false, "",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff}, 7, h.newStatus.ID)
	require.Error(t, err)

	require.Empty(t, h.store.replies[tk.ID], "the reply committed without its audit entry")
	require.Empty(t, h.auditStore.entries)
}

func TestAddGuestReply_AuditEntryHasNoActor(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()

	_, err := h.svc.AddGuestReply(context.Background(), tk.ID, "still broken", 7, h.newStatus.ID)
	require.NoError(t, err)

	got := replyEntries(h, tk.ID)
	require.Len(t, got, 1)
	require.Equal(t, ticket.AuditActionReplyAdded, got[0].Action)
	require.Nil(t, got[0].ActorID, "a guest has no account, as for every other action they cause")
}

func TestAddReply_RequesterReplyActorIsTheRequester(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()

	_, err := h.svc.AddReply(context.Background(), tk.ID, "hi", false, false, "",
		ticket.Actor{UserID: tk.ReporterUserID, Role: user.RoleUser}, 7, h.newStatus.ID)
	require.NoError(t, err)

	got := replyEntries(h, tk.ID)
	require.Len(t, got, 1)
	require.Equal(t, *tk.ReporterUserID, *got[0].ActorID)
}

// #541: the closure runs again after a killed attempt. The first attempt's
// entry rolled back with it, so the committed trail holds exactly one.
func TestAddReply_RetryWritesOneAuditEntry(t *testing.T) {
	h := newHarness(t)
	tk := h.seedOpen()
	svc := h.rerunService(t, func() {})
	agent := uuid.New()

	_, err := svc.AddReply(context.Background(), tk.ID, "hello", true, false, "",
		ticket.Actor{UserID: &agent, Role: user.RoleStaff}, 7, h.newStatus.ID)
	require.NoError(t, err)

	require.Len(t, replyEntries(h, tk.ID), 1, "a retried reply wrote its audit entry twice")
	require.Len(t, h.store.replies[tk.ID], 1)
}

// A long conversation must not push the ticket's real history (created, status
// changes) off the feed's cap: reply and note entries are capped on their own,
// like follow entries.
func TestListAuditFeed_RepliesNeverPushHistoryOffTheCap(t *testing.T) {
	h := newHarness(t)
	id := uuid.New()
	base := time.Now().Add(-24 * time.Hour)
	h.auditStore.entries = append(h.auditStore.entries,
		audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: "created", CreatedAt: base},
		audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: "resolved", CreatedAt: base.Add(time.Hour)})
	for i := 0; i < 700; i++ {
		a := ticket.AuditActionReplyAdded
		if i%2 == 1 {
			a = ticket.AuditActionNoteAdded
		}
		h.auditStore.entries = append(h.auditStore.entries,
			audit.Entry{ID: uuid.New(), EntityType: "ticket", EntityID: id, Action: a, CreatedAt: base.Add(2*time.Hour + time.Duration(i)*time.Second)})
	}

	got, err := h.svc.ListAuditFeed(context.Background(), id, false)
	require.NoError(t, err)
	require.Equal(t, "created", got[0].Action, "the conversation pushed the ticket's history off the feed")
	require.Equal(t, "resolved", got[1].Action)
	require.Len(t, got, 2+500, "replies are capped on their own, newest kept")
}
