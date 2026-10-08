package server_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #349 over MCP, through the real tools and the real transport. The rule is
// the service's, so the surfaces cannot disagree about it (the previous
// incident, GHSA-2x4f-j4jv-m2cm, was two surfaces each deciding one rule).

// Under the DEFAULT closed_reopen_policy (off); the policy-on MCP cases are in
// handler_closed_reopen_policy_test.go.
func TestMCP_ClosedIsReadOnlyForRequestersAndTerminalForEveryone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterClosedTicket(t, h)
	newStatus := statusIDNamed(t, h, ticket.StatusNameNew).String()
	id := tk.ID.String()

	t.Run("a reporting user can write nothing, on their own closed ticket", func(t *testing.T) {
		c := openMCP(t, h, h.userKey)
		defer c.closeBody()
		for tool, args := range map[string]map[string]any{
			"add_reply":            {"ticket_id": id, "body": "still broken"},
			"update_ticket_status": {"ticket_id": id, "status_id": newStatus},
			"assign_ticket":        {"ticket_id": id, "assignee_user_id": h.staffID.String()},
			"create_follow_up":     {"ticket_id": id},
		} {
			got := c.call(t, tool, args)
			require.Contains(t, got, `"isError":true`, "%s: %s", tool, got)
			require.Contains(t, got, "staff", "%s must say a staff account is needed: %s", tool, got)
		}
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
		replies, err := h.ticketSvc.ListReplies(context.Background(), tk.ID)
		require.NoError(t, err)
		require.Empty(t, replies)
	})

	for who, key := range map[string]string{"staff": h.apiKey, "admin": h.adminKey} {
		t.Run(who+" cannot reopen a closed ticket", func(t *testing.T) {
			c := openMCP(t, h, key)
			defer c.closeBody()
			for _, status := range []string{ticket.StatusNameNew, ticket.StatusNameResolved} {
				got := c.call(t, "update_ticket_status", map[string]any{
					"ticket_id": id, "status_id": statusIDNamed(t, h, status).String(),
				})
				require.Contains(t, got, `"isError":true`, got)
				require.Contains(t, got, "closed", "the refusal must say why: %s", got)
				require.Contains(t, got, "follow-up", "and where to go instead: %s", got)
			}
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
		})
	}

	// Decision (DESIGN.md → Closing): staff keep replying to and annotating a
	// closed ticket; only leaving Closed is removed.
	t.Run("staff can still reply to a closed ticket, and it stays closed", func(t *testing.T) {
		c := openMCP(t, h, h.apiKey)
		defer c.closeBody()
		got := c.call(t, "add_reply", map[string]any{"ticket_id": id, "body": "for the record", "internal": true})
		require.NotContains(t, got, `"isError":true`, got)
		require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, tk.ID))
	})
}

func TestMCP_CreateFollowUp(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	closed := reporterClosedTicket(t, h)
	open, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Still open", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	for who, key := range map[string]string{"staff": h.apiKey, "admin": h.adminKey} {
		t.Run(who+" opens a linked ticket from a closed one", func(t *testing.T) {
			c := openMCP(t, h, key)
			defer c.closeBody()
			got := c.call(t, "create_follow_up", map[string]any{"ticket_id": closed.ID.String()})
			require.NotContains(t, got, `"isError":true`, got)
			require.Contains(t, got, closed.Subject)

			links, err := h.ticketSvc.ListLinks(ctx, closed.ID)
			require.NoError(t, err)
			require.NotEmpty(t, links)
			last := links[len(links)-1]
			require.Equal(t, closed.ID, last.SourceTicketID)
			require.Equal(t, ticket.LinkParentChild, last.LinkType)
			require.Equal(t, "New", statusOf(t, h, last.TargetTicketID), "a follow-up starts open")
			require.Equal(t, ticket.StatusNameClosed, statusOf(t, h, closed.ID), "the original stays closed")
		})
	}

	t.Run("a ticket that is not closed is refused", func(t *testing.T) {
		c := openMCP(t, h, h.apiKey)
		defer c.closeBody()
		got := c.call(t, "create_follow_up", map[string]any{"ticket_id": open.ID.String()})
		require.Contains(t, got, `"isError":true`, got)
		require.Contains(t, got, "closed", got)
		links, err := h.ticketSvc.ListLinks(ctx, open.ID)
		require.NoError(t, err)
		require.Empty(t, links)
	})

	t.Run("a ticket that does not exist answers like one the caller cannot see", func(t *testing.T) {
		c := openMCP(t, h, h.apiKey)
		defer c.closeBody()
		got := c.call(t, "create_follow_up", map[string]any{"ticket_id": "6f1c1e50-0000-4000-8000-000000000000"})
		require.Contains(t, got, "not found")
	})
}
