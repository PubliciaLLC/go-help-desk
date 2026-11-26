package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mcpserver "github.com/publiciallc/go-help-desk/backend/internal/mcp"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #711: ONE policy decides which levels of the tree a caller sees, and every
// surface asks it. These tests fail if any surface keeps a rule of its own.
//
// The expectations below are written out by caller and by setting, not read from
// catalogue.VisibleLevels: a test that asked the function would agree with a
// function that is wrong.

type policyCaller struct {
	name string
	// who: "guest", "requester" or "staff": what the policy must say.
	who string
	get func(path string) *http.Response
	// mcpKey is the API key to open an MCP connection with, or "" for no MCP
	// (a guest has none, and an OAuth client's token does not open the SSE one).
	mcpKey string
}

type policySettings struct{ sub, guestTypes, guestItems, usersItems bool }

func (p policySettings) String() string {
	return fmt.Sprintf("submission=%v guestTypes=%v guestItems=%v usersItems=%v", p.sub, p.guestTypes, p.guestItems, p.usersItems)
}

func allPolicySettings() []policySettings {
	var out []policySettings
	for _, a := range []bool{false, true} {
		for _, b := range []bool{false, true} {
			for _, c := range []bool{false, true} {
				for _, d := range []bool{false, true} {
					out = append(out, policySettings{a, b, c, d})
				}
			}
		}
	}
	return out
}

func (p policySettings) apply(t *testing.T, h *harness) {
	t.Helper()
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, p.sub)
	setChoice(t, h, guestsTypesKey, p.guestTypes)
	setChoice(t, h, guestsItemsKey, p.guestItems)
	setChoice(t, h, usersItemKey, p.usersItems)
}

// want is the levels the caller kind must see, spelled out.
func (p policySettings) want(who string) (types, items bool) {
	switch who {
	case "staff":
		return true, true
	case "requester":
		return true, p.usersItems
	}
	types = p.sub && p.guestTypes
	return types, types && p.guestItems
}

func mcpText(t *testing.T, payload string) string {
	t.Helper()
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &rpc), payload)
	require.NotEmpty(t, rpc.Result.Content, payload)
	return rpc.Result.Content[0].Text
}

// GET with a guest link's Authorization header and nothing else.
func (h *harness) getAsGuestToken(t *testing.T, token, path string) *http.Response {
	return h.doGuest(t, http.MethodGet, path, token, nil)
}

// The REST catalogue and the MCP catalogue agree, for every caller and every
// combination of the settings.
func TestCataloguePolicy_EverySurfaceAgrees(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)

	resp := h.createCredential(t, "/api/v1/admin/oauth-clients", map[string]any{"name": "levels", "scopes": []string{"tickets:read"}})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	oauth := h.oauthToken(t, created.ClientID, created.ClientSecret)
	_, guestToken := seedGuestTicket(t, h) // a guest link; also switches guest submission on
	reporter := h.session(t, asReporter)
	staffSess := h.session(t, asStaff)

	callers := []policyCaller{
		{"guest", "guest", func(p string) *http.Response { return h.doUnauth(t, http.MethodGet, p, nil) }, ""},
		{"guest link", "guest", func(p string) *http.Response { return h.getAsGuestToken(t, guestToken, p) }, ""},
		{"requester session", "requester", func(p string) *http.Response { return h.doSession(t, reporter, http.MethodGet, p, nil) }, ""},
		{"requester API key", "requester", func(p string) *http.Response { return h.doAsUser(t, http.MethodGet, p, nil) }, h.userKey},
		{"staff session", "staff", func(p string) *http.Response { return h.doSession(t, staffSess, http.MethodGet, p, nil) }, ""},
		{"staff API key", "staff", func(p string) *http.Response { return h.do(t, http.MethodGet, p, nil) }, h.apiKey},
		{"admin API key", "staff", func(p string) *http.Response { return h.doAsAdmin(t, http.MethodGet, p, nil) }, h.adminKey},
		// An OAuth client acts as its service user, who is staff.
		{"OAuth client", "staff", func(p string) *http.Response { return h.doWithBearer(t, oauth, p) }, ""},
	}
	conns := map[string]*mcpConn{}
	for _, c := range callers {
		if c.mcpKey != "" && conns[c.mcpKey] == nil {
			conns[c.mcpKey] = openMCP(t, h, c.mcpKey)
			defer conns[c.mcpKey].closeBody()
		}
	}

	for _, set := range allPolicySettings() {
		set.apply(t, h)
		for _, c := range callers {
			t.Run(c.name+"/"+set.String(), func(t *testing.T) {
				wantTypes, wantItems := set.want(c.who)

				// A guest of an instance with submission off is not served at all.
				if c.who == "guest" && !set.sub {
					res := c.get(tree.typesPath())
					res.Body.Close()
					require.Equal(t, http.StatusUnauthorized, res.StatusCode)
					return
				}

				types := listNames(t, c.get(tree.typesPath()))
				items := listNames(t, c.get(tree.itemsPath()))
				require.Equal(t, wantTypes, len(types) > 0, "REST types")
				require.Equal(t, wantItems, len(items) > 0, "REST items")
				if wantItems {
					require.Contains(t, items, tree.itemName)
				}
				// The category list is always served.
				require.Contains(t, listNames(t, c.get("/api/v1/categories")), "Hardware")

				if c.mcpKey == "" {
					return
				}
				text := mcpText(t, conns[c.mcpKey].call(t, "list_categories", map[string]any{}))
				require.Contains(t, text, "Hardware")
				require.Equal(t, wantTypes, strings.Contains(text, tree.typeName), "MCP types")
				require.Equal(t, wantItems, strings.Contains(text, tree.itemName), "MCP items")
			})
		}
	}
}

// ── what a requester reads of a ticket ───────────────────────────────────────

func reporterTicketWithItem(t *testing.T, h *harness, tree choiceTree) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Dock dead", Description: "x", CategoryID: tree.cat, TypeID: idp(tree.typ), ItemID: idp(tree.item),
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	return tk
}

func jsonKeys(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m), string(body))
	return m
}

// A ticket that has an Item shows none to its requester while Items is not
// visible to them, in the ticket, the list and over MCP; it shows again when the
// switch is turned back on, and staff always see it. The switch flipped on to off
// takes effect on tickets that already carry an item.
func TestCataloguePolicy_RequesterTicketsCarryNoItemWhileItemsIsOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	tk := reporterTicketWithItem(t, h, tree)
	reporter := h.session(t, asReporter)
	mcp := openMCP(t, h, h.userKey)
	defer mcp.closeBody()

	one := func(do func(string) *http.Response) map[string]json.RawMessage {
		res := do("/api/v1/tickets/" + tk.ID.String())
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, string(b))
		return jsonKeys(t, b)
	}
	list := func(do func(string) *http.Response) []map[string]json.RawMessage {
		res := do("/api/v1/tickets")
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, string(b))
		var rows []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(b, &rows), string(b))
		require.NotEmpty(t, rows)
		return rows
	}
	session := func(p string) *http.Response { return h.doSession(t, reporter, http.MethodGet, p, nil) }
	key := func(p string) *http.Response { return h.doAsUser(t, http.MethodGet, p, nil) }
	staff := func(p string) *http.Response { return h.do(t, http.MethodGet, p, nil) }

	check := func(itemVisible bool) {
		t.Helper()
		for name, do := range map[string]func(string) *http.Response{"session": session, "API key": key} {
			_, has := one(do)["item_id"]
			require.Equal(t, itemVisible, has, "GET /tickets/{id} as a requester %s", name)
			require.Contains(t, one(do), "type_id", "the type stays")
			for _, row := range list(do) {
				_, has := row["item_id"]
				require.Equal(t, itemVisible, has, "GET /tickets row as a requester %s", name)
			}
		}
		got := mcpText(t, mcp.call(t, "get_ticket", map[string]any{"id": tk.ID.String()}))
		require.Equal(t, itemVisible, strings.Contains(got, `"item_id"`), "MCP get_ticket")
		got = mcpText(t, mcp.call(t, "list_tickets", map[string]any{}))
		require.Equal(t, itemVisible, strings.Contains(got, `"item_id"`), "MCP list_tickets")
		_, has := one(staff)["item_id"]
		require.True(t, has, "staff always see the item")
	}

	check(false)
	setChoice(t, h, usersItemKey, true)
	check(true)
	setChoice(t, h, usersItemKey, false)
	check(false) // flipped on to off: a ticket that had an item shows none again

	// The dashboard's ticket lists go through the same exit.
	res := h.doAsUser(t, http.MethodGet, "/api/v1/dashboard", nil)
	require.NotContains(t, string(readAll(t, res)), tree.item.String())
}

// What the requester gets back when they file a ticket goes through the same
// exit: the item they chose is in the answer only while Items is visible.
func TestCataloguePolicy_CreateAnswerFollowsTheLevels(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, usersItemKey, true)
	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Contains(t, jsonKeys(t, body), "item_id")

	setChoice(t, h, usersItemKey, false)
	code, body = requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.NotContains(t, jsonKeys(t, body), "item_id")
	require.Contains(t, jsonKeys(t, body), "type_id")
}

// Filtering on an item is refused to a requester who cannot see Items, over REST
// and over MCP, and is allowed once they can; staff may always.
func TestCataloguePolicy_ItemFilterIsRefusedWithoutItems(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	reporterTicketWithItem(t, h, tree)
	mcp := openMCP(t, h, h.userKey)
	defer mcp.closeBody()
	staffMCP := openMCP(t, h, h.apiKey)
	defer staffMCP.closeBody()
	reporter := h.session(t, asReporter)

	path := "/api/v1/tickets?item_id=" + tree.item.String()
	typePath := "/api/v1/tickets?type_id=" + tree.typ.String()
	status := func(res *http.Response) int { res.Body.Close(); return res.StatusCode }

	require.Equal(t, http.StatusForbidden, status(h.doAsUser(t, http.MethodGet, path, nil)))
	require.Equal(t, http.StatusForbidden, status(h.doSession(t, reporter, http.MethodGet, path, nil)))
	require.Equal(t, http.StatusOK, status(h.doAsUser(t, http.MethodGet, typePath, nil)), "the type filter stays")
	require.Equal(t, http.StatusOK, status(h.do(t, http.MethodGet, path, nil)), "staff")
	res := h.doAsUser(t, http.MethodGet, path, nil)
	require.Contains(t, string(readAll(t, res)), "item_id is not available")

	got := mcp.call(t, "list_tickets", map[string]any{"item_id": tree.item.String()})
	require.Contains(t, got, "item_id is not available")
	require.NotContains(t, mcpText(t, mcp.call(t, "list_tickets", map[string]any{"type_id": tree.typ.String()})), "not available")
	require.NotContains(t, staffMCP.call(t, "list_tickets", map[string]any{"item_id": tree.item.String()}), "not available")

	setChoice(t, h, usersItemKey, true)
	require.Equal(t, http.StatusOK, status(h.doAsUser(t, http.MethodGet, path, nil)))
	require.Equal(t, http.StatusOK, status(h.doSession(t, reporter, http.MethodGet, path, nil)))
	require.NotContains(t, mcp.call(t, "list_tickets", map[string]any{"item_id": tree.item.String()}), "not available")
}

// A requester who cannot see Items cannot ask for the fields of an item either:
// the answer is the category's and type's, and an item-level assignment, which
// carries the item's id, is not in it. Over REST and over MCP.
func TestCataloguePolicy_ItemFieldsAreNotResolvedWithoutItems(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildChoiceTree(t, h)
	def := h.mkField(t, "Dock serial", customfield.FieldTypeText, nil, 1)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{FieldDefID: def.ID, ScopeType: customfield.ItemScope, ScopeID: tree.item, VisibleOnNew: true}, nil)
	require.NoError(t, err)
	mcp := openMCPOn(t, h, h.userKey, mcpserver.New(h.ticketSvc, nil, h.srv, h.categorySvc, h.srv, h.srv).WithCustomFields(h.customFieldSvc))
	defer mcp.closeBody()

	path := "/api/v1/tickets/fields?category_id=" + tree.cat.String() + "&type_id=" + tree.typ.String() + "&item_id=" + tree.item.String()
	read := func(do func(*testing.T, string, string, any) *http.Response) string {
		res := do(t, http.MethodGet, path, nil)
		return string(readAll(t, res))
	}
	mcpArgs := map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()}

	require.NotContains(t, read(h.doAsUser), tree.item.String())
	require.NotContains(t, read(h.doAsUser), "Dock serial")
	require.NotContains(t, mcpText(t, mcp.call(t, "list_ticket_fields", mcpArgs)), "Dock serial")
	require.Contains(t, read(h.do), "Dock serial", "staff")

	setChoice(t, h, usersItemKey, true)
	require.Contains(t, read(h.doAsUser), "Dock serial")
	require.Contains(t, mcpText(t, mcp.call(t, "list_ticket_fields", mcpArgs)), "Dock serial")
}
