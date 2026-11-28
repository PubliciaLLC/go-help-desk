package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #625, closed-ticket follow-up limits 1, 3, 4 and 6 through the real routes.

func getJSON(t *testing.T, res *http.Response) map[string]any {
	t.Helper()
	body := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out
}

// Item 6: the ticket page can tell a closed ticket already has a follow-up.
func TestTicketView_AClosedTicketCarriesItsFollowUp(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := reporterClosedTicket(t, h)
	path := "/api/v1/tickets/" + tk.ID.String()

	t.Run("none yet", func(t *testing.T) {
		require.Nil(t, getJSON(t, h.doAsUser(t, http.MethodGet, path, nil))["follow_up"])
		require.Nil(t, getJSON(t, h.do(t, http.MethodGet, path, nil))["follow_up"])
	})

	made := decodeTicket(t, h.doAsUser(t, http.MethodPost, path+"/follow-up", nil))

	t.Run("the requester and staff are both shown it", func(t *testing.T) {
		for who, do := range map[string]func(*testing.T, string, string, any) *http.Response{
			"requester": h.doAsUser, "staff": h.do,
		} {
			got := getJSON(t, do(t, http.MethodGet, path, nil))
			fu, ok := got["follow_up"].(map[string]any)
			require.True(t, ok, "%s: follow_up is an object: %v", who, got["follow_up"])
			require.Equal(t, made.ID.String(), fu["id"], who)
			require.Equal(t, string(made.TrackingNumber), fu["tracking_number"], who)
			require.Len(t, fu, 2, who)
		}
	})

	t.Run("the follow-up itself, open, has none", func(t *testing.T) {
		require.Nil(t, getJSON(t, h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+made.ID.String(), nil))["follow_up"])
	})

	t.Run("a closed ticket with only a link made by hand has none", func(t *testing.T) {
		other := reporterClosedTicket(t, h)
		third, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "Unrelated", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID})
		require.NoError(t, err)
		require.NoError(t, h.ticketSvc.AddLink(ctx, other.ID, third.ID, ticket.LinkParentChild,
			ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}))
		require.Nil(t, getJSON(t, h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+other.ID.String(), nil))["follow_up"])

		// Item 3 end to end: the link does not use up the requester's one follow-up.
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+other.ID.String()+"/follow-up", nil)
		decodeTicket(t, res)
	})
}

// Item 6, the guest's side: a boolean, never the new ticket.
func TestGuestTicket_SaysWhetherAFollowUpWasOpened(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	tk, token := seedGuestTicket(t, h)

	require.Equal(t, false, getJSON(t, h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil))["has_follow_up"],
		"an open ticket has none")
	closeGuestTicket(t, h, tk)
	require.Equal(t, false, getJSON(t, h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil))["has_follow_up"])

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	created := readBody(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, created)

	res = h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Equal(t, true, got["has_follow_up"])
	require.NotContains(t, got, "follow_up", "the guest is not given the new ticket")
	var made map[string]string
	require.NoError(t, json.Unmarshal([]byte(created), &made))
	require.NotContains(t, body, made["tracking_number"])
}

// Item 1: the guest's mail for a follow-up is the follow-up mail.
func TestGuestFollowUp_TheCreatedEventSaysItIsAFollowUp(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	tk, token := seedGuestTicket(t, h)
	closeGuestTicket(t, h, tk)
	h.dispatcher.record = true

	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var saw bool
	for _, ev := range h.dispatcher.queued {
		if ev.Type == notification.EventTicketCreated {
			saw = true
			require.True(t, ev.FollowUp)
		}
	}
	require.True(t, saw)
}

// Item 4: a follow-up of a ticket whose reporter's account was deleted is a 409
// with its own code, not the 400 of a validation error.
func TestFollowUp_ADeletedReporterIsA409(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	gone, err := h.userSvc.Create(ctx, userInput("gone@test.local", "Gone"))
	require.NoError(t, err)
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Left the company", Description: "x", CategoryID: h.catID, ReporterUserID: &gone.ID})
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	res := h.doAsAdminSession(t, http.MethodDelete, "/api/v1/admin/users/"+gone.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res = h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusConflict, res.StatusCode, body)
	require.Contains(t, body, "reporter_deleted")
	require.Contains(t, body, "open a new ticket", "it says what to do instead")

	t.Run("over MCP", func(t *testing.T) {
		c := openMCP(t, h, h.apiKey)
		defer c.closeBody()
		got := c.call(t, "create_follow_up", map[string]any{"ticket_id": tk.ID.String()})
		require.Contains(t, got, `"isError":true`, got)
		require.Contains(t, got, "deleted")
	})

	links, err := h.ticketSvc.ListLinks(ctx, tk.ID)
	require.NoError(t, err)
	require.Empty(t, links, "nothing was created")
}
