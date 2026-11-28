package server_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// MCP list_tickets takes `scope` and `held_by` (#676) through the same
// ticket.ResolveQueue as GET /tickets. The strongest test of "the same" is to
// run the list fixture through both, as the same caller, under every setting
// that changes an answer, and compare the ticket ids in order and the
// refusals in words. The fixture is newListFixture:
//
//	a  assigned to staff           e  assigned to staff
//	b  group mine                  f  unassigned, reported by staff
//	c  group other                 g  assigned to admin
//	d  unassigned                  h  group other

var listScopes = []string{"mine", "assigned_to_me", "my_groups", "unassigned", "all", "visible"}

func TestMCP_ListTickets_ScopeAndHeldByAgreeWithREST(t *testing.T) {
	configs := []struct {
		name     string
		ticked   bool // Staff ticked under Available
		enforced bool // ticket_scope_enforced
		covered  bool // the staff member's group covers the category (enforced only)
	}{
		{"not ticked, enforcement off", false, false, false},
		{"ticked, enforcement off", true, false, false},
		{"not ticked, enforcement on", false, true, true},
		{"ticked, enforcement on, nothing covered", true, true, false},
		{"ticked, enforcement on, category covered", true, true, true},
	}
	callers := []struct {
		as    string // the name listAs takes
		token func(*harness) string
	}{
		{"staff", func(h *harness) string { return h.apiKey }},
		{"admin", func(h *harness) string { return h.adminKey }},
	}
	// Every scope, each alone and with each held_by, and held_by on the default.
	type ask struct{ scope, heldBy string }
	var asks []ask
	for _, sc := range append([]string{""}, listScopes...) {
		for _, hb := range []string{"", "nobody", "me", "others"} {
			asks = append(asks, ask{sc, hb})
		}
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			fx := newListFixture(t, h)
			if cfg.ticked {
				setAvailableRoles(t, h, `["staff"]`)
			}
			if cfg.enforced {
				enableScope(t, h)
				if cfg.covered {
					require.NoError(t, h.groupSvc.AddScope(context.Background(), group.GroupScope{GroupID: fx.mine, CategoryID: h.catID}))
				}
			}

			for _, tr := range mcpTransports {
				for _, who := range callers {
					c := tr.open(t, h, who.token(h))
					for _, a := range asks {
						name := tr.name + "/" + who.as + "/scope=" + a.scope + "/held_by=" + a.heldBy
						t.Run(name, func(t *testing.T) {
							q, args := url.Values{}, map[string]any{}
							if a.scope != "" {
								q.Set("scope", a.scope)
								args["scope"] = a.scope
							} else {
								// What an MCP caller who sends no scope has always got is REST's visible.
								q.Set("scope", "visible")
							}
							if a.heldBy != "" {
								q.Set("held_by", a.heldBy)
								args["held_by"] = a.heldBy
							}
							rest := h.listAs(t, who.as, "?"+q.Encode())
							ids, refusal := mcpList(t, c, args)

							if rest.status != http.StatusOK {
								require.Equal(t, http.StatusForbidden, rest.status, "REST: %s", rest.raw)
								require.Equal(t, rest.message, refusal, "the same refusal, in the same words")
								return
							}
							require.Empty(t, refusal)
							require.Equal(t, rest.ids(), ids, "MCP and REST must list the same tickets in the same order")
						})
					}
				}
			}
		})
	}
}

// The parity test above would pass if both sides were wrong the same way, so
// this holds what the queues are, over MCP, with enforcement off.
func TestMCP_ListTickets_QueuesAreWhatTheDesignSays(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	letters := func(c mcpCaller, args map[string]any) string {
		t.Helper()
		ids, refusal := mcpList(t, c, args)
		require.Empty(t, refusal)
		got := ""
		for _, l := range "abcdefgh" {
			for _, id := range ids {
				if fx.byName[string(l)].ID == id {
					got += string(l)
				}
			}
		}
		require.Len(t, ids, len(got), "every id is a fixture ticket")
		return got
	}
	staff := mcpTransports[0].open(t, h, h.apiKey)
	adm := mcpTransports[0].open(t, h, h.adminKey)

	require.Equal(t, "abcdefgh", letters(staff, map[string]any{}), "no scope is visible, as it always was")
	require.Equal(t, "abcdefgh", letters(staff, map[string]any{"scope": "visible"}))
	require.Equal(t, "abe", letters(staff, map[string]any{"scope": "mine"}))
	require.Equal(t, "ae", letters(staff, map[string]any{"scope": "assigned_to_me"}))
	require.Equal(t, "b", letters(staff, map[string]any{"scope": "my_groups"}))
	require.Equal(t, "b", letters(staff, map[string]any{"scope": "my_groups", "held_by": "nobody"}))
	require.Equal(t, "", letters(staff, map[string]any{"scope": "my_groups", "held_by": "me"}))
	require.Equal(t, "ae", letters(staff, map[string]any{"held_by": "me"}))
	require.Equal(t, "bcdfh", letters(staff, map[string]any{"held_by": "nobody"}))
	require.Equal(t, "g", letters(staff, map[string]any{"held_by": "others"}))
	require.Equal(t, "", letters(staff, map[string]any{"scope": "my_groups", "assignee_group_id": fx.other.String()}),
		"a group the caller is not in narrows my_groups to nothing and never widens it")
	require.Equal(t, "ch", letters(staff, map[string]any{"assignee_group_id": fx.other.String()}),
		"assignee_group_id is not refused to staff outside the group, as before")

	_, refusal := mcpList(t, staff, map[string]any{"scope": "unassigned"})
	require.Equal(t, refusedScope, refusal)
	_, refusal = mcpList(t, staff, map[string]any{"scope": "all"})
	require.Equal(t, refusedScope, refusal)
	_, refusal = mcpList(t, staff, map[string]any{"scope": "bogus"})
	require.Contains(t, refusal, "invalid scope")
	_, refusal = mcpList(t, staff, map[string]any{"scope": ""})
	require.Contains(t, refusal, "invalid scope")

	require.Equal(t, "df", letters(adm, map[string]any{"scope": "unassigned"}))
	require.Equal(t, "abcdefgh", letters(adm, map[string]any{"scope": "all"}))
	require.Equal(t, "g", letters(adm, map[string]any{"scope": "assigned_to_me"}))
	require.Equal(t, "", letters(adm, map[string]any{"scope": "my_groups"}))

	setAvailableRoles(t, h, `["staff"]`)
	require.Equal(t, "df", letters(staff, map[string]any{"scope": "unassigned"}), "ticking Staff opens Available")
	_, refusal = mcpList(t, staff, map[string]any{"scope": "all"})
	require.Equal(t, refusedScope, refusal, "and nothing else")
}

// A requester has no queues and no held_by. Over MCP too.
func TestMCP_ListTickets_RequesterHasNoQueues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	setAvailableRoles(t, h, `["admin","staff"]`)
	c := mcpTransports[0].open(t, h, h.userKey)

	for _, scope := range []string{"mine", "assigned_to_me", "my_groups", "visible"} {
		ids, refusal := mcpList(t, c, map[string]any{"scope": scope})
		require.Empty(t, refusal, scope)
		require.ElementsMatch(t, []string{fx.byName["a"].ID, fx.byName["b"].ID, fx.byName["c"].ID, fx.byName["d"].ID}, ids,
			"%s: their own tickets", scope)
	}
	for _, scope := range []string{"unassigned", "all"} {
		_, refusal := mcpList(t, c, map[string]any{"scope": scope})
		require.Equal(t, refusedScope, refusal, scope)
	}
	_, refusal := mcpList(t, c, map[string]any{"held_by": "me"})
	require.Equal(t, "held_by is not available to requesters", refusal)
}
