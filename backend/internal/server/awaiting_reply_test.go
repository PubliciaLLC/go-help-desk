package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// awaiting_reply (#675) over REST and MCP. The fixture is newListFixture (a-d
// reported by the requester, e, g and h by the administrator, f by staff),
// then a few public replies:
//
//	a  staff replied           -> not waiting
//	b  staff, then requester   -> waiting
//	c  no reply                -> waiting (the description is the requester's)
//	d  administrator replied   -> not waiting
//	e-h no reply               -> waiting
const refusedAwaiting = "awaiting_reply is not available to requesters"

func newAwaitingFixture(t *testing.T, h *harness) listFixture {
	t.Helper()
	fx := newListFixture(t, h)
	say := func(as, name string) {
		path := "/api/v1/tickets/" + fx.byName[name].ID + "/replies"
		body := map[string]any{"body": "reply on " + name}
		var res *http.Response
		switch as {
		case "user":
			res = h.doAsUser(t, http.MethodPost, path, body)
		case "admin":
			res = h.doAsAdmin(t, http.MethodPost, path, body)
		default:
			res = h.do(t, http.MethodPost, path, body)
		}
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", readAll(t, res))
	}
	say("staff", "a")
	say("staff", "b")
	say("user", "b")
	say("admin", "d")
	return fx
}

func TestListTickets_AwaitingReply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newAwaitingFixture(t, h)

	runListCases(t, h, fx, []listCase{
		{name: "staff: waiting", as: "staff", query: "?scope=visible&awaiting_reply=true", want: "bcefgh"},
		{name: "admin: waiting", as: "admin", query: "?scope=all&awaiting_reply=true", want: "bcefgh"},
		{name: "false is the same as absent", as: "staff", query: "?scope=visible&awaiting_reply=false", want: "abcdefgh"},
		{name: "empty is the same as absent", as: "staff", query: "?scope=visible&awaiting_reply=", want: "abcdefgh"},
		{name: "narrowed by another filter", as: "staff", query: "?scope=visible&awaiting_reply=true&held_by=nobody", want: "bcfh"},
		{name: "a requester is refused", as: "user", query: "?awaiting_reply=true", status: 403, code: "forbidden", message: refusedAwaiting},
		{name: "a requester may send false", as: "user", query: "?awaiting_reply=false", want: "abcd"},
		{name: "maybe is a bad request", as: "staff", query: "?awaiting_reply=maybe", status: 400, code: "bad_request", message: "invalid awaiting_reply"},
		{name: "maybe is a bad request for a requester too", as: "user", query: "?awaiting_reply=maybe", status: 400, code: "bad_request", message: "invalid awaiting_reply"},
	})
}

func TestMCP_ListTickets_AwaitingReply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newAwaitingFixture(t, h)

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			staff := tr.open(t, h, h.apiKey)
			admin := tr.open(t, h, h.adminKey)
			user := tr.open(t, h, h.userKey)

			for _, who := range []struct {
				as string
				c  mcpCaller
			}{{"staff", staff}, {"admin", admin}} {
				rest := h.listAs(t, who.as, "?scope=visible&awaiting_reply=true")
				require.Equal(t, http.StatusOK, rest.status, "REST: %s", rest.raw)
				got, refusal := mcpList(t, who.c, map[string]any{"awaiting_reply": true})
				require.Empty(t, refusal)
				require.Equal(t, rest.ids(), got, "%s: MCP and REST must list the same tickets in the same order", who.as)

				all, refusal := mcpList(t, who.c, map[string]any{})
				require.Empty(t, refusal)
				off, refusal := mcpList(t, who.c, map[string]any{"awaiting_reply": false})
				require.Empty(t, refusal)
				require.Equal(t, all, off, "%s: false is the same as no argument", who.as)
				require.Greater(t, len(all), len(got), "%s: the filter narrows", who.as)
			}

			for name, v := range map[string]any{"string": "true", "number": 1.0, "object": map[string]any{}} {
				_, refusal := mcpList(t, staff, map[string]any{"awaiting_reply": v})
				require.Contains(t, refusal, "invalid awaiting_reply", name)
			}

			_, refusal := mcpList(t, user, map[string]any{"awaiting_reply": true})
			require.Equal(t, refusedAwaiting, refusal)
			ids, refusal := mcpList(t, user, map[string]any{"awaiting_reply": false})
			require.Empty(t, refusal)
			require.Len(t, ids, 4, "a requester may send false")
		})
	}
}
