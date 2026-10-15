package mcp

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every MCP tool is classified by whether it can put a ticket in its answer. A
// tool that can must send it through ticketResult (the requester allowed list),
// and is driven as a requester by TestRequesterResponses_* in internal/server.
// A new tool fails here until it is classified, so it cannot skip that check.
func TestEveryToolIsClassifiedForRequesterTicketFields(t *testing.T) {
	returnsTickets, noTickets := map[string]bool{}, map[string]bool{}
	for _, n := range TicketTools() {
		returnsTickets[n] = true
	}
	for _, n := range NonTicketTools() {
		noTickets[n] = true
	}

	s := New(nil, nil, nil, nil)
	var got []string
	for name := range s.mcp.ListTools() {
		got = append(got, name)
		require.True(t, returnsTickets[name] || noTickets[name],
			"tool %q is not classified: if it can return a ticket, route it through ticketResult and add it to the requester test in internal/server, then list it here", name)
	}
	sort.Strings(got)
	require.Len(t, got, len(returnsTickets)+len(noTickets), "a classified tool is no longer registered: %v", got)
}
