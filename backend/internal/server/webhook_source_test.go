package server_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// The source a redelivery reads the ticket through, against the real service
// and database: current values, public replies only, and a ticket that is not
// there is reported as gone and not as an error.
func TestWebhookEventSource_ReadsTheTicketAsItIsNow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)
	src := server.NewWebhookEventSource(h.ticketSvc)

	newStatus := statusIDNamed(t, h, ticket.StatusNameNew)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	pub, err := h.ticketSvc.AddReply(ctx, tk.ID, "PUBLIC TEXT", ticket.TextFormatPlain, false, false, "", staff, 7, newStatus)
	require.NoError(t, err)
	note, err := h.ticketSvc.AddReply(ctx, tk.ID, "INTERNAL TEXT", ticket.TextFormatPlain, true, false, "", staff, 7, newStatus)
	require.NoError(t, err)

	facts, found, err := src.TicketFacts(ctx, tk.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, tk.Subject, facts.Subject)
	require.Equal(t, string(tk.TrackingNumber), facts.TrackingNumber)
	require.NotEmpty(t, facts.StatusName, "the status is named, not an id")
	// What a webhook filter tests (#413), read from the ticket as it is now.
	require.Equal(t, &notification.TicketState{
		CategoryID: tk.CategoryID, Priority: string(tk.Priority), AssigneeGroupID: tk.AssigneeGroupID,
	}, facts.State)

	body, ok, err := src.PublicReplyBody(ctx, tk.ID, pub.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "PUBLIC TEXT", body)

	body, ok, err = src.PublicReplyBody(ctx, tk.ID, note.ID)
	require.NoError(t, err)
	require.False(t, ok, "an internal note's text is never returned")
	require.Empty(t, body)

	_, ok, err = src.PublicReplyBody(ctx, tk.ID, uuid.New())
	require.NoError(t, err)
	require.False(t, ok)

	other, _ := seedGuestTicket(t, h)
	_, ok, err = src.PublicReplyBody(ctx, other.ID, pub.ID)
	require.NoError(t, err)
	require.False(t, ok, "a reply of another ticket is not this ticket's")

	_, found, err = src.TicketFacts(ctx, uuid.New())
	require.NoError(t, err)
	require.False(t, found)
}

// The names a created-ticket chat message shows (placement), read through the
// production source from the ticket's ids, with the real service and database.
// Renamed since the ids were taken: the name now.
func TestWebhookEventSource_PlacementNamesTheIdsAsTheyAreNow(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.makeCTITree(t)
	email := "guest@test.local"
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Battery swollen", Description: "bulging", CategoryID: h.catID, TypeID: &c.typ, ItemID: &c.item,
		GuestEmail: &email, GuestName: "Ada"})
	require.NoError(t, err)
	src := server.NewWebhookEventSource(h.ticketSvc)

	facts, found, err := src.TicketFacts(ctx, tk.ID)
	require.NoError(t, err)
	require.True(t, found)
	got, err := src.Placement(ctx, *facts.State)
	require.NoError(t, err)
	require.Equal(t, "Laptop", got.Type)
	require.Equal(t, "Battery", got.Item)
	require.NotEmpty(t, got.Category)
	require.Empty(t, got.Group)
	require.Empty(t, got.Assignee)

	require.NoError(t, h.categorySvc.UpdateItem(ctx, category.Item{ID: c.item, TypeID: c.typ, Name: "Battery pack", SortOrder: 1, Active: true}))
	got, err = src.Placement(ctx, *facts.State)
	require.NoError(t, err)
	require.Equal(t, "Battery pack", got.Item, "named as it is when the message is built")
}

// A replay names the holder too, so the state it reads from the ticket carries
// the assignee as well as the group.
func TestWebhookEventSource_StateCarriesTheAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk, _ := seedGuestTicket(t, h)
	_, err := h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)
	src := server.NewWebhookEventSource(h.ticketSvc)

	facts, found, err := src.TicketFacts(ctx, tk.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, facts.State.AssigneeUserID)
	require.Equal(t, h.staffID, *facts.State.AssigneeUserID)

	got, err := src.Placement(ctx, *facts.State)
	require.NoError(t, err)
	require.Equal(t, "Staff", got.Assignee)
}
