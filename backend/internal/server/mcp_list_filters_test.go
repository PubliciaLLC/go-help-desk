package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
)

// MCP list_tickets takes the filters GET /tickets takes, through the same
// parser and the same check (advanced-search.md section 5). The strongest test
// of "the same" is to ask both for the same thing and compare, in order.

// mcpList calls list_tickets and decodes the tickets, or returns the refusal.
func mcpList(t *testing.T, c mcpCaller, args map[string]any) (ids []string, refusal string) {
	t.Helper()
	text, isErr := mcpToolText(t, c.call(t, "list_tickets", args))
	if isErr {
		return nil, text
	}
	var out []listedTicket
	require.NoError(t, json.Unmarshal([]byte(text), &out), "list_tickets said: %s", text)
	ids = []string{}
	for _, tk := range out {
		ids = append(ids, tk.ID)
	}
	return ids, ""
}

func (h *harness) setPriority(t *testing.T, ticketID, priority string) {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+ticketID, map[string]any{"priority": priority})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", readAll(t, res))
}

func TestMCP_ListTickets_FiltersAgreeWithREST(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	h.setPriority(t, fx.byName["a"].ID, "high")
	h.setPriority(t, fx.byName["e"].ID, "critical")
	h.setPriority(t, fx.byName["f"].ID, "low")

	var statuses []string
	for _, s := range mustStatuses(t, h) {
		statuses = append(statuses, s.ID.String())
	}
	closed := statusIDNamed(t, h, ticket.StatusNameClosed).String()
	ownStatus := func(letter string) string {
		id, err := uuid.Parse(fx.byName[letter].ID)
		require.NoError(t, err)
		tk, err := h.ticketSvc.GetByID(context.Background(), id)
		require.NoError(t, err)
		return tk.StatusID.String()
	}

	cases := []struct {
		name  string
		args  map[string]any
		query url.Values
	}{
		{"reporter_id", map[string]any{"reporter_id": h.userID.String()}, url.Values{"reporter_id": {h.userID.String()}}},
		{"requester free text", map[string]any{"requester": "reporting user"}, url.Values{"requester": {"reporting user"}}},
		{"requester free text on a guest-less address", map[string]any{"requester": "nobody-at-all"}, url.Values{"requester": {"nobody-at-all"}}},
		{"priority as a string", map[string]any{"priority": "high"}, url.Values{"priority": {"high"}}},
		{"priority as an array", map[string]any{"priority": []any{"high", "low"}}, url.Values{"priority": {"high", "low"}}},
		{"status_ids", map[string]any{"status_ids": []any{statuses[0], closed}}, url.Values{"status_id": {statuses[0], closed}}},
		{"status_ids naming only Closed", map[string]any{"status_ids": []any{closed}}, url.Values{"status_id": {closed}}},
		{"status_id, the single form", map[string]any{"status_id": ownStatus("a")}, url.Values{"status_id": {ownStatus("a")}}},
		{"include_closed false", map[string]any{"include_closed": false}, url.Values{"include_closed": {"false"}}},
		{"source_channel", map[string]any{"source_channel": []any{"api", "web"}}, url.Values{"source_channel": {"api", "web"}}},
		{"created_after", map[string]any{"created_after": "2000-01-01T00:00:00Z"}, url.Values{"created_after": {"2000-01-01T00:00:00Z"}}},
		{"created_before in the past", map[string]any{"created_before": "2000-01-01T00:00:00Z"}, url.Values{"created_before": {"2000-01-01T00:00:00Z"}}},
		{"updated_after", map[string]any{"updated_after": "2000-01-01T00:00:00Z"}, url.Values{"updated_after": {"2000-01-01T00:00:00Z"}}},
		{"assignee_user_id", map[string]any{"assignee_user_id": h.staffID.String()}, url.Values{"assignee_user_id": {h.staffID.String()}}},
		{"assignee_group_id", map[string]any{"assignee_group_id": fx.other.String()}, url.Values{"assignee_group_id": {fx.other.String()}}},
		{"category_id", map[string]any{"category_id": h.catID.String()}, url.Values{"category_id": {h.catID.String()}}},
		{"q", map[string]any{"q": "alpaca"}, url.Values{"q": {"alpaca"}}},
		{"filters combine", map[string]any{"q": "llama", "priority": []any{"critical", "low"}, "include_closed": false},
			url.Values{"q": {"llama"}, "priority": {"critical", "low"}, "include_closed": {"false"}}},
		{"sort by subject, ascending", map[string]any{"sort": "subject", "order": "asc"}, url.Values{"sort": {"subject"}, "order": {"asc"}}},
		{"sort by subject, descending", map[string]any{"sort": "subject", "order": "desc"}, url.Values{"sort": {"subject"}, "order": {"desc"}}},
		{"sort by priority", map[string]any{"sort": "priority"}, url.Values{"sort": {"priority"}}},
		{"sort by priority, least urgent first", map[string]any{"sort": "priority", "order": "desc"}, url.Values{"sort": {"priority"}, "order": {"desc"}}},
		{"sort by number", map[string]any{"sort": "number", "order": "desc"}, url.Values{"sort": {"number"}, "order": {"desc"}}},
		{"a search sorted by a column", map[string]any{"q": "llama", "sort": "created", "order": "asc"},
			url.Values{"q": {"llama"}, "sort": {"created"}, "order": {"asc"}}},
	}

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.apiKey)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					// MCP has always listed everything the caller may see, which is REST's
					// scope=visible; REST's default for staff is the queue assigned to them.
					tc.query.Set("scope", "visible")
					rest := h.listAs(t, "staff", "?"+tc.query.Encode())
					require.Equal(t, http.StatusOK, rest.status, "REST: %s", rest.raw)

					got, refusal := mcpList(t, c, tc.args)
					require.Empty(t, refusal)
					require.Equal(t, rest.ids(), got, "MCP and REST must list the same tickets in the same order")
				})
			}
		})
	}

	// The cases above are only worth anything if they tell the filters apart.
	t.Run("the fixture distinguishes the filters", func(t *testing.T) {
		c := mcpTransports[0].open(t, h, h.apiKey)
		all, _ := mcpList(t, c, map[string]any{})
		high, _ := mcpList(t, c, map[string]any{"priority": "high"})
		both, _ := mcpList(t, c, map[string]any{"priority": []any{"high", "low"}})
		asc, _ := mcpList(t, c, map[string]any{"sort": "subject", "order": "asc"})
		desc, _ := mcpList(t, c, map[string]any{"sort": "subject", "order": "desc"})
		none, _ := mcpList(t, c, map[string]any{"status_ids": []any{closed}})
		require.Len(t, all, 8)
		require.Len(t, high, 1)
		require.Len(t, both, 2)
		require.NotEqual(t, asc, desc)
		require.Empty(t, none)
	})
}

// A requester may use only the filters they are sent the fields of. Over MCP
// the refusal is the answer, in REST's words, and is not an error in the log.
func TestMCP_ListTickets_RequesterKeyCannotUseStaffOnlyFilters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	staffOnly := map[string]map[string]any{
		"reporter_id":       {"reporter_id": h.userID.String()},
		"requester":         {"requester": "reporting"},
		"updated_after":     {"updated_after": "2000-01-01T00:00:00Z"},
		"updated_before":    {"updated_before": "2100-01-01T00:00:00Z"},
		"assignee_user_id":  {"assignee_user_id": h.staffID.String()},
		"assignee_group_id": {"assignee_group_id": uuid.NewString()},
		"source_channel":    {"source_channel": []any{"api"}},
		"sla":               {"sla": "breached"},
		"sort=updated":      {"sort": "updated"},
		"sort=sla":          {"sort": "sla"},
	}
	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.userKey)
			for name, args := range staffOnly {
				t.Run(name, func(t *testing.T) {
					logs := &syncBuffer{}
					prev := slog.Default()
					slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
					t.Cleanup(func() { slog.SetDefault(prev) })

					ids, refusal := mcpList(t, c, args)
					require.Empty(t, ids)
					require.Contains(t, refusal, "is not available to requesters")
					require.NotContains(t, logs.String(), "level=ERROR")
				})
			}

			t.Run("what they may use works, and is still their own tickets", func(t *testing.T) {
				ids, refusal := mcpList(t, c, map[string]any{"priority": []any{"medium", "high"}, "sort": "subject", "order": "desc"})
				require.Empty(t, refusal)
				rest := h.listAs(t, "user", "?priority=medium&priority=high&sort=subject&order=desc")
				require.Equal(t, rest.ids(), ids)
				require.Len(t, ids, 4, "the four tickets the requester filed, and none of the others")
			})
		})
	}
}

// Free-text requester search needs requester_search:read on a machine
// credential, over MCP as over REST, and tickets:read does not carry it.
func TestMCP_ListTickets_RequesterTextNeedsTheScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	without := mintKey(t, h, []string{"tickets:read"})
	writeOnly := mintKey(t, h, []string{"tickets:write"})
	with := mintKey(t, h, []string{"tickets:read", "requester_search:read"})
	args := map[string]any{"requester": "reporting user"}

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			for name, token := range map[string]string{"tickets:read": without, "tickets:write": writeOnly} {
				t.Run("refused with "+name, func(t *testing.T) {
					logs := &syncBuffer{}
					prev := slog.Default()
					slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
					t.Cleanup(func() { slog.SetDefault(prev) })

					ids, refusal := mcpList(t, tr.open(t, h, token), args)
					require.Empty(t, ids)
					require.Equal(t, "this credential does not carry the requester_search:read scope", refusal)
					require.NotContains(t, logs.String(), "level=ERROR")
				})
			}

			t.Run("allowed with the scope", func(t *testing.T) {
				ids, refusal := mcpList(t, tr.open(t, h, with), args)
				require.Empty(t, refusal)
				require.Equal(t, h.listAs(t, "admin", "?requester=reporting+user&scope=visible").ids(), ids)
				require.NotEmpty(t, ids)
			})

			t.Run("the id filter needs no such scope", func(t *testing.T) {
				ids, refusal := mcpList(t, tr.open(t, h, without), map[string]any{"reporter_id": h.userID.String()})
				require.Empty(t, refusal)
				require.NotEmpty(t, ids)
			})
		})
	}
}

// A refusal for a filter the caller has no right to is a 403 on REST and a
// refusal here. A mistake in what was sent is a refusal too, and neither is a
// server fault.
func TestMCP_ListTickets_RefusesWhatItCannotRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"an unknown sort", map[string]any{"sort": "bogus"}, "invalid sort"},
		{"relevance is not a sort", map[string]any{"sort": "relevance"}, "invalid sort"},
		{"an unknown order", map[string]any{"sort": "subject", "order": "sideways"}, "invalid order"},
		{"an unknown order without a sort", map[string]any{"order": "sideways"}, "invalid order"},
		{"an unknown channel", map[string]any{"source_channel": []any{"pigeon"}}, "invalid source_channel"},
		{"a bad status in a list", map[string]any{"status_ids": []any{"nope"}}, "invalid status_id"},
		{"a range that runs backwards", map[string]any{"created_after": "2026-04-01T00:00:00Z", "created_before": "2026-03-01T00:00:00Z"},
			"created_after must be before created_before"},
		{"requester text too short", map[string]any{"requester": "x"}, "requester must be 2 to 254 characters"},
		{"sla while tracking is not on", map[string]any{"sla": "breached"}, "SLA tracking is off"},
	}
	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.apiKey)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					logs := &syncBuffer{}
					prev := slog.Default()
					slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
					t.Cleanup(func() { slog.SetDefault(prev) })

					ids, refusal := mcpList(t, c, tc.args)
					require.Empty(t, ids)
					require.Contains(t, refusal, tc.want)
					require.NotContains(t, refusal, "list tickets failed")
					require.NotContains(t, logs.String(), "level=ERROR")
				})
			}
		})
	}
}

// With SLA tracking on, the sla filter and sort are served.
func TestMCP_ListTickets_SLAFilterWorksWhenTrackingIsOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	on := mcp.New(h.ticketSvc, func(context.Context) string { return "GHD" }, h.srv, h.categorySvc, h.srv, h.srv).
		WithSLAEnabled(func(context.Context) (bool, error) { return true, nil })
	c := openMCPOn(t, h, h.apiKey, on)
	defer c.closeBody()

	ids, refusal := mcpList(t, c, map[string]any{"sla": "breached"})
	require.Empty(t, refusal)
	require.Empty(t, ids, "nothing in the fixture has an SLA record, let alone a breached one")

	ids, refusal = mcpList(t, c, map[string]any{"sort": "sla"})
	require.Empty(t, refusal)
	require.Len(t, ids, 8)
}

// The page size is unchanged: 20 unless asked, at most 100.
func TestMCP_ListTickets_LimitIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for i := 0; i < 25; i++ {
		_, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
			DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "bulk", Description: "x", CategoryID: h.catID,
			Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
	}
	c := mcpTransports[0].open(t, h, h.apiKey)

	ids, _ := mcpList(t, c, map[string]any{})
	require.Len(t, ids, 20, "the default is 20")
	ids, _ = mcpList(t, c, map[string]any{"limit": 500})
	require.Len(t, ids, 25, "the ceiling is 100, and there are 25")
	ids, _ = mcpList(t, c, map[string]any{"limit": 5, "offset": 20, "sort": "number"})
	require.Len(t, ids, 5, "offset still pages a sorted listing")
}

func mustStatuses(t *testing.T, h *harness) []ticket.Status {
	t.Helper()
	sts, err := h.ticketSvc.ListStatuses(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, sts)
	return sts
}
