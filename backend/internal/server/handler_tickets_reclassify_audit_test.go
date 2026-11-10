package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// PATCH /tickets/{id} {category_id} is audited (1.3.0 audit D3): the feed says
// who moved the ticket and between which categories, by id, and the audit view
// does not show the ticket's free text beside it.
func TestUpdateTicket_Reclassify_IsAuditedHTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	other, err := h.categorySvc.CreateCategory(ctx, "Reclassify target", 91)
	require.NoError(t, err)
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Printer", CategoryID: h.catID, Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	path := "/api/v1/tickets/" + tk.ID.String()

	res := h.do(t, http.MethodPatch, path, map[string]any{"category_id": other.ID.String()})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)

	res = h.doAsAdmin(t, http.MethodGet, path+"/audit", nil)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var entries []struct {
		Action  string         `json:"action"`
		ActorID *string        `json:"actor_id"`
		Before  map[string]any `json:"before"`
		After   map[string]any `json:"after"`
	}
	require.NoError(t, json.Unmarshal(b, &entries))
	var found bool
	for _, e := range entries {
		if e.Action != "classified" {
			continue
		}
		found = true
		require.NotNil(t, e.ActorID)
		require.Equal(t, h.staffID.String(), *e.ActorID)
		require.Equal(t, h.catID.String(), e.Before["category_id"])
		require.Equal(t, other.ID.String(), e.After["category_id"])
		require.Equal(t, "sla_off", e.After["sla_outcome"], "the harness never switches the SLA on")
		require.NotContains(t, e.After, "subject", "free text stays out of the feed")
	}
	require.True(t, found, "entries: %s", b)

	// A request that changes nothing adds nothing.
	res = h.do(t, http.MethodPatch, path, map[string]any{"category_id": other.ID.String()})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	after, err := h.ticketSvc.ListAuditFeed(ctx, tk.ID, true)
	require.NoError(t, err)
	var n int
	for _, e := range after {
		if e.Action == "classified" {
			n++
		}
	}
	require.Equal(t, 1, n)
}
