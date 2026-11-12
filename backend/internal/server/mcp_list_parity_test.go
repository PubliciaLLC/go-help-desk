package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The rest of the MCP-versus-REST comparison: the filters and sorts the first
// fixture cannot tell apart (it files eight tickets in one category and one
// status). This one gives type, item, status, update time and reply text
// something to split, and every filter case below must return some of the
// tickets and not all of them, or it would pass whatever the filter did.
func TestMCP_ListTickets_AgreesWithRESTOnTypeItemStatusTimeAndReplies(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	typeA, err := h.categorySvc.CreateType(ctx, h.catID, "Hardware", 1)
	require.NoError(t, err)
	itemA, err := h.categorySvc.CreateItem(ctx, typeA.ID, "Printer", 1)
	require.NoError(t, err)
	typeB, err := h.categorySvc.CreateType(ctx, h.catID, "Network", 2)
	require.NoError(t, err)

	file := func(subject string, typeID, itemID *uuid.UUID) string {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: subject, Description: "nothing relevant", CategoryID: h.catID,
			TypeID: typeID, ItemID: itemID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		return tk.ID.String()
	}
	t1 := file("one", &typeA.ID, &itemA.ID)
	t2 := file("two", &typeA.ID, nil)
	t3 := file("three", &typeB.ID, nil)
	t4 := file("four", nil, nil)
	t5 := file("five", nil, nil)
	t6 := file("six", nil, nil)

	// Statuses sort by their sort_order and then their name, and the two new
	// ones share a sort_order, so Aardvark precedes Zebra and both follow the
	// statuses every ticket starts in.
	aardvark, zebra := mkStatus(t, h, "Aardvark queue"), mkStatus(t, h, "Zebra queue")
	setStatus := func(id, status string) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+id, map[string]any{"status_id": status})
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", readAll(t, res))
	}
	setStatus(t2, zebra)
	setStatus(t5, aardvark)
	setStatus(t3, zebra)
	// Updates in an order that is not the order they were filed in, so that
	// sorting by updated and by created are different listings.
	h.setPriority(t, t4, "medium")
	h.setPriority(t, t1, "high")
	h.setPriority(t, t6, "high")

	var three struct {
		UpdatedAt string `json:"updated_at"`
	}
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+t3, nil)
	require.NoError(t, json.Unmarshal(readAll(t, res), &three))

	// Three more tickets, filed last, with the words in their replies.
	newMCPReplyFixture(t, h)

	cases := []struct {
		name  string
		args  map[string]any
		query url.Values
		split bool // some of the tickets, not all and not none
	}{
		{"type_id", map[string]any{"type_id": typeA.ID.String()}, url.Values{"type_id": {typeA.ID.String()}}, true},
		{"another type_id", map[string]any{"type_id": typeB.ID.String()}, url.Values{"type_id": {typeB.ID.String()}}, true},
		{"item_id", map[string]any{"item_id": itemA.ID.String()}, url.Values{"item_id": {itemA.ID.String()}}, true},
		{"updated_before", map[string]any{"updated_before": three.UpdatedAt}, url.Values{"updated_before": {three.UpdatedAt}}, true},
		{"updated_after", map[string]any{"updated_after": three.UpdatedAt}, url.Values{"updated_after": {three.UpdatedAt}}, true},
		{"a status", map[string]any{"status_ids": []any{zebra}}, url.Values{"status_id": {zebra}}, true},
		{"statuses", map[string]any{"status_ids": []any{zebra, aardvark}}, url.Values{"status_id": {zebra, aardvark}}, true},
		{"a search that reaches only the replies", map[string]any{"q": mcpWordPublic, "search_replies": true},
			url.Values{"q": {mcpWordPublic}, "search_replies": {"true"}}, true},
		{"the same search without search_replies", map[string]any{"q": mcpWordPublic},
			url.Values{"q": {mcpWordPublic}}, false},
		{"search_replies false", map[string]any{"q": mcpWordPublic, "search_replies": false},
			url.Values{"q": {mcpWordPublic}, "search_replies": {"false"}}, false},
		{"sort by status, ascending", map[string]any{"sort": "status", "order": "asc"}, url.Values{"sort": {"status"}, "order": {"asc"}}, false},
		{"sort by status, descending", map[string]any{"sort": "status", "order": "desc"}, url.Values{"sort": {"status"}, "order": {"desc"}}, false},
		{"sort by status, natural order", map[string]any{"sort": "status"}, url.Values{"sort": {"status"}}, false},
		{"sort by created, ascending", map[string]any{"sort": "created", "order": "asc"}, url.Values{"sort": {"created"}, "order": {"asc"}}, false},
		{"sort by created, descending", map[string]any{"sort": "created", "order": "desc"}, url.Values{"sort": {"created"}, "order": {"desc"}}, false},
		{"sort by updated, ascending", map[string]any{"sort": "updated", "order": "asc"}, url.Values{"sort": {"updated"}, "order": {"asc"}}, false},
		{"sort by updated, descending", map[string]any{"sort": "updated", "order": "desc"}, url.Values{"sort": {"updated"}, "order": {"desc"}}, false},
	}

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.apiKey)
			all, refusal := mcpList(t, c, map[string]any{})
			require.Empty(t, refusal)
			require.Len(t, all, 9)

			byName := map[string][]string{}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					q := url.Values{"scope": {"visible"}}
					for k, v := range tc.query {
						q[k] = v
					}
					rest := h.listAs(t, "staff", "?"+q.Encode())
					require.Equal(t, http.StatusOK, rest.status, "REST: %s", rest.raw)

					got, refusal := mcpList(t, c, tc.args)
					require.Empty(t, refusal)
					require.Equal(t, rest.ids(), got, "MCP and REST must list the same tickets in the same order")
					byName[tc.name] = got

					if tc.split {
						require.NotEmpty(t, got, "a case that matches nothing proves nothing")
						require.Less(t, len(got), len(all), "a case that matches everything proves nothing")
					}
				})
			}

			// Ascending and descending are different listings, and the
			// fixture's updated order is not its created order.
			require.NotEqual(t, byName["sort by status, ascending"], byName["sort by status, descending"])
			require.NotEqual(t, byName["sort by created, ascending"], byName["sort by created, descending"])
			require.NotEqual(t, byName["sort by updated, ascending"], byName["sort by updated, descending"])
			require.NotEqual(t, byName["sort by created, ascending"], byName["sort by updated, ascending"])
			require.Len(t, byName["a search that reaches only the replies"], 2, "the two tickets with the word in a public reply")
			require.Empty(t, byName["the same search without search_replies"], "off unless asked for")
		})
	}
}
