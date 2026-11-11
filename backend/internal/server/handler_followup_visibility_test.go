package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A link marked follow_up that points at somebody else's ticket must not tell
// the closed ticket's viewer that ticket's id or tracking number (#625 review).
// Such a link can exist: migration 000064 marked every parent_child link that
// existed, hand-made ones between unrelated tickets included.
func foreignFollowUpLink(t *testing.T, h *harness, closedID, reporter uuid.UUID) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	foreign, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Somebody else's", Description: "x", CategoryID: h.catID, ReporterUserID: &reporter})
	require.NoError(t, err)
	require.NoError(t, h.ticketStore.CreateLink(ctx, ticket.TicketLink{
		SourceTicketID: closedID, TargetTicketID: foreign.ID, LinkType: ticket.LinkParentChild, FollowUp: true}))
	return foreign
}

func TestTicketView_AFollowUpLinkToSomeoneElsesTicketIsNotShown(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := raisedClosedTicket(t, h) // the requester's, assigned to the harness staff member
	foreign := foreignFollowUpLink(t, h, tk.ID, h.adminID)
	path := "/api/v1/tickets/" + tk.ID.String()

	t.Run("the requester", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodGet, path, nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.NotContains(t, body, "follow_up")
		require.NotContains(t, body, foreign.ID.String())
		require.NotContains(t, body, string(foreign.TrackingNumber))
	})

	t.Run("staff who cannot see the target", func(t *testing.T) {
		enableScope(t, h)
		res := h.do(t, http.MethodGet, path, nil)
		body := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.NotContains(t, body, foreign.ID.String())
		require.NotContains(t, body, string(foreign.TrackingNumber))
	})
}

func TestTicketView_TheOwnerStillSeesTheirFollowUpBesideAForeignLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterClosedTicket(t, h)
	made := decodeTicket(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil))
	foreignFollowUpLink(t, h, tk.ID, h.adminID) // possibly newer than the real one

	got := getJSON(t, h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String(), nil))
	fu, ok := got["follow_up"].(map[string]any)
	require.True(t, ok, "the requester still sees their own follow-up: %v", got["follow_up"])
	require.Equal(t, made.ID.String(), fu["id"])
}

func TestGuestTicket_AFollowUpLinkToSomeoneElsesTicketIsNotCounted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableGuests(t, h)
	tk, token := seedGuestTicket(t, h)
	closeGuestTicket(t, h, tk)

	t.Run("an account holder's ticket", func(t *testing.T) {
		foreignFollowUpLink(t, h, tk.ID, h.adminID)
		require.Equal(t, false, getJSON(t, h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil))["has_follow_up"])
	})

	t.Run("another guest's ticket", func(t *testing.T) {
		other, _ := seedGuestTicketFor(t, h, "someone-else@test.local")
		require.NoError(t, h.ticketStore.CreateLink(context.Background(), ticket.TicketLink{
			SourceTicketID: tk.ID, TargetTicketID: other.ID, LinkType: ticket.LinkParentChild, FollowUp: true}))
		require.Equal(t, false, getJSON(t, h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil))["has_follow_up"])
	})

	t.Run("the guest's own follow-up still counts", func(t *testing.T) {
		own, ownToken := seedGuestTicketFor(t, h, "owner@test.local")
		closeGuestTicket(t, h, own)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", ownToken, nil)
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
		foreignFollowUpLink(t, h, own.ID, h.adminID) // beside a foreign link
		require.Equal(t, true, getJSON(t, h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", ownToken, nil))["has_follow_up"])
	})
}
