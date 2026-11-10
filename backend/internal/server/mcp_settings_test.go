package server_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// add_reply used to pass a constant 7 as the reopen window. A staff reply never
// consults it today, so the constant was harmless and invisible, and it would
// have become a silent disagreement with the setting the moment anything did.
// The tool now asks the setting each call, as the REST reply handler does.
func TestMCP_AddReplyReadsTheReopenWindowSetting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterOpenTicket(t, h)

	var asked atomic.Int32
	m := mcp.New(h.ticketSvc, nil, h.srv, h.categorySvc, h.srv).
		WithReopenWindowDays(func(context.Context) int { asked.Add(1); return 3 })
	c := openMCPOn(t, h, h.apiKey, m)
	defer c.closeBody()

	got := c.call(t, "add_reply", map[string]any{"ticket_id": tk.ID.String(), "body": "looking into it"})
	require.NotContains(t, got, `"isError":true`, got)
	require.Equal(t, int32(1), asked.Load(), "add_reply must read the reopen window from the setting")
}
