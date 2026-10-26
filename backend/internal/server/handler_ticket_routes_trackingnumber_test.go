package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Every route under /tickets/{id} accepts a tracking number as well as a UUID
// (#420). GET /tickets/{id} always did, and requireTicketAccess resolves both
// for the whole subtree, but most handlers then parsed the path again as a UUID
// and answered 400 for a tracking number, under two different codes
// (bad_request and invalid_id) depending on the file. The assertion is that
// the two spellings of the same ticket get the same answer, on a fresh ticket
// each so a state change on one cannot colour the other.
func TestTicketRoutes_AcceptTrackingNumbersEverywhere(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	newTicket := func(subject string) ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: subject, Description: "x", CategoryID: h.catID,
			ReporterUserID: &h.staffID, TrackingPrefix: "GHD",
		})
		require.NoError(t, err)
		return tk
	}
	other := newTicket("link target")

	routes := []struct {
		method, suffix string
		body           any
	}{
		{http.MethodGet, "", nil},
		{http.MethodPatch, "", map[string]any{}},
		{http.MethodPost, "/replies", map[string]any{"body": "hello"}},
		{http.MethodGet, "/replies", nil},
		{http.MethodPost, "/resolve", map[string]any{}},
		{http.MethodPost, "/close", map[string]any{}},
		{http.MethodPost, "/reopen", map[string]any{}},
		{http.MethodPost, "/follow-up", map[string]any{}},
		{http.MethodPost, "/links", map[string]any{"target_id": other.ID, "link_type": "related_to"}},
		{http.MethodDelete, "/links/" + other.ID.String() + "/related_to", nil},
		{http.MethodGet, "/links", nil},
		{http.MethodGet, "/history", nil},
		{http.MethodGet, "/audit", nil},
		{http.MethodGet, "/tags", nil},
		{http.MethodPost, "/tags", map[string]any{"name": "fraud-suspect"}},
		{http.MethodDelete, "/tags/" + uuid.NewString(), nil},
		{http.MethodGet, "/canned-responses", nil},
		{http.MethodGet, "/attachments", nil},
		{http.MethodPost, "/attachments", nil},
		{http.MethodGet, "/attachments/" + uuid.NewString(), nil},
		{http.MethodPost, "/attachments/" + uuid.NewString() + "/reputation", nil},
		{http.MethodGet, "/custom-fields", nil},
		{http.MethodPut, "/custom-fields", map[string]any{}},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.suffix, func(t *testing.T) {
			byUUID := newTicket("by uuid")
			byNumber := newTicket("by number")

			res := h.doAsAdmin(t, rt.method, "/api/v1/tickets/"+byUUID.ID.String()+rt.suffix, rt.body)
			res.Body.Close()
			want := res.StatusCode

			res = h.doAsAdmin(t, rt.method, "/api/v1/tickets/"+string(byNumber.TrackingNumber)+rt.suffix, rt.body)
			res.Body.Close()
			require.Equal(t, want, res.StatusCode,
				fmt.Sprintf("%s %s: a tracking number must get the answer a UUID gets", rt.method, rt.suffix))
		})
	}
}
