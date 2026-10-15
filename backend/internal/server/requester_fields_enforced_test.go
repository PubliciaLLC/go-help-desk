package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// One definition of what a requester or a guest may see of a ticket
// (ticket.RequesterTicketKeys, Erik 2026-10-15), enforced where responses leave
// the server. This test does not trust that every route was wrapped: it calls
// EVERY route the router serves as a requester, against a ticket with every
// field filled, and fails if a key from the never list turns up anywhere in any
// body, or a ticket-shaped object carries a key outside the allowed list.
//
// Write routes are called with an empty body, which mostly answers 400; the ones
// that return a ticket on success (create, follow-up) are driven again with a
// real body below. MCP is not on the router, so its tools are driven by name in
// TestRequesterResponses_MCP; mcp's own test fails when a tool is not classified.

type fieldRules struct {
	never   map[string]bool
	allowed map[string]bool // keys a ticket-shaped object may carry
}

func requesterRules() fieldRules {
	r := fieldRules{never: map[string]bool{}, allowed: map[string]bool{}}
	for _, k := range ticket.RequesterNeverTicketKeys() {
		r.never[k] = true
	}
	for _, k := range append(ticket.RequesterTicketKeys(), ticket.RequesterWrapperKeys()...) {
		r.allowed[k] = true
	}
	return r
}

func guestRules() fieldRules {
	r := requesterRules()
	r.allowed = map[string]bool{"status": true, "replies": true}
	for _, k := range ticket.GuestTicketKeys() {
		r.allowed[k] = true
	}
	return r
}

// scan walks decoded JSON. A "ticket-shaped" object is one with a tracking
// number. It reports whether it saw one.
func (r fieldRules) scan(t *testing.T, label string, v any) (sawTicket bool) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		_, isTicket := x["tracking_number"]
		for k, child := range x {
			// updated_at and closed_at are ordinary names (a user has an updated_at
			// too), so they are checked inside a ticket-shaped object only.
			if r.never[k] && (isTicket || (k != "updated_at" && k != "closed_at")) {
				require.Fail(t, "key must never reach this viewer", "%s: key %q", label, k)
			}
			if isTicket {
				require.True(t, r.allowed[k], "%s: ticket carries %q, which is not on the allowed list", label, k)
			}
			if r.scan(t, label, child) {
				sawTicket = true
			}
		}
		return sawTicket || isTicket
	case []any:
		for _, e := range x {
			if r.scan(t, label, e) {
				sawTicket = true
			}
		}
	}
	return sawTicket
}

func (r fieldRules) scanBody(t *testing.T, label string, body []byte) bool {
	t.Helper()
	var v any
	if json.Unmarshal(body, &v) != nil {
		return false // not JSON: a file, a redirect, an empty body
	}
	return r.scan(t, label, v)
}

// everyFieldTicket files a ticket for the requester and fills every field a
// ticket has, including the ones only staff set.
func everyFieldTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	ty, err := h.categorySvc.CreateType(ctx, h.catID, "T"+uuid.NewString()[:6], 1)
	require.NoError(t, err)
	it, err := h.categorySvc.CreateItem(ctx, ty.ID, "I"+uuid.NewString()[:6], 1)
	require.NoError(t, err)
	grp, err := h.groupSvc.Create(ctx, "G"+uuid.NewString()[:6], "d")
	require.NoError(t, err)

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Every field", Description: "all of them", CategoryID: h.catID, TypeID: &ty.ID, ItemID: &it.ID,
		Priority: ticket.PriorityHigh, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	notes := "fixed it"
	tk.AssigneeUserID, tk.AssigneeGroupID = &h.staffID, &grp.ID
	tk.ResolutionNotes, tk.ResolvedAt, tk.ClosedAt = &notes, &now, &now
	tk.PendingSince, tk.SLAPausedSeconds, tk.ReopenCount = &now, 120, 2
	tk.LastReopenedAt, tk.PausedSecondsAtLastReopen = &now, 60
	require.NoError(t, h.ticketStore.Update(ctx, tk))
	return tk
}

func TestRequesterResponses_NoRouteLeaksAFieldOutsideTheAllowedList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tk := everyFieldTicket(t, h)
	other, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Linked", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.AddLink(ctx, tk.ID, other.ID, ticket.LinkRelatedTo, staff))
	newStatus := statusIDNamed(t, h, ticket.StatusNameNew)
	_, err = h.ticketSvc.AddReply(ctx, tk.ID, "public answer", false, true, "", staff, 7, newStatus)
	require.NoError(t, err)

	rules := requesterRules()
	routes := h.srv.RoutesForTest()
	require.Greater(t, len(routes), 100, "the walk found almost no routes, so it is not looking at the router")

	called, sawTickets := 0, 0
	byRoute := map[string]bool{}
	for _, rt := range routes {
		if !strings.HasPrefix(rt.Pattern, "/api/v1/") || strings.Contains(rt.Pattern, "*") ||
			strings.HasPrefix(rt.Pattern, "/api/v1/auth/") {
			continue
		}
		path := rt.Pattern
		for _, sub := range [][2]string{{"{id}", tk.ID.String()}, {"{targetId}", other.ID.String()}, {"{linkType}", "related_to"}} {
			path = strings.ReplaceAll(path, sub[0], sub[1])
		}
		for strings.Contains(path, "{") {
			i, j := strings.Index(path, "{"), strings.Index(path, "}")
			path = path[:i] + uuid.NewString() + path[j+1:]
		}
		var body any
		if rt.Method != http.MethodGet && rt.Method != http.MethodHead {
			body = map[string]any{}
		}
		res := h.doAsUser(t, rt.Method, path, body)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if rt.Method == http.MethodGet && rt.Pattern == "/api/v1/tickets/{id}/" {
			// The one ticket a requester reads is exactly the allowed list plus the
			// wrapper keys: nothing on it is missing (assignee_user_id is kept) and
			// nothing else is there (no closed_at, no updated_at).
			var got map[string]any
			require.NoError(t, json.Unmarshal(b, &got))
			keys := []string{}
			for k := range got {
				keys = append(keys, k)
			}
			// Spelled out, not read from the list under test.
			require.ElementsMatch(t, []string{
				"assignee_user_id", "category_id", "created_at", "description", "id", "item_id", "priority",
				"resolution_notes", "resolved_at", "status_id", "subject", "tracking_number", "type_id",
				"sla", "can_reopen",
			}, keys)
		}
		called++
		if rules.scanBody(t, rt.Method+" "+rt.Pattern, b) {
			sawTickets++
			byRoute[rt.Method+" "+rt.Pattern] = true
		}
	}
	require.Greater(t, called, 60, "too few routes were called to mean anything")
	// The routes known to return tickets were among those that did, so the scan
	// was looking at real ticket bodies.
	for _, want := range []string{"GET /api/v1/tickets/", "GET /api/v1/tickets/{id}/", "PATCH /api/v1/tickets/{id}/"} {
		require.True(t, byRoute[want], "%s did not return a ticket-shaped body to the requester; got %v", want, byRoute)
	}
	require.GreaterOrEqual(t, sawTickets, 3)
}

// The write routes that return a ticket on success need a body that succeeds.
func TestRequesterResponses_WritesThatReturnATicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	rules := requesterRules()

	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "New one", "description": "x", "category_id": h.catID.String(),
	})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, string(b))
	require.True(t, rules.scanBody(t, "POST /tickets", b))

	tk := everyFieldTicket(t, h)
	admin := ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, admin))
	res = h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", map[string]any{})
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, string(b))
	require.True(t, rules.scanBody(t, "POST /tickets/{id}/follow-up", b))
}

// A guest: every guest route, against a ticket with the guest's own fields.
func TestGuestResponses_NoRouteLeaksAFieldOutsideTheGuestList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	rules := guestRules()

	var routes []string
	for _, rt := range h.srv.RoutesForTest() {
		if strings.HasPrefix(rt.Pattern, "/api/v1/guest/") {
			routes = append(routes, rt.Method+" "+rt.Pattern)
		}
	}
	require.GreaterOrEqual(t, len(routes), 5, "the walk found almost no guest routes: %v", routes)

	res := h.doGuest(t, http.MethodGet, "/api/v1/guest/ticket", token, nil)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, string(b))
	require.True(t, rules.scanBody(t, "GET /guest/ticket", b))
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	keys := []string{}
	for k := range got {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{"tracking_number", "subject", "description", "status", "created_at", "replies"}, keys)
	_ = tk

	for _, rt := range routes {
		method, pattern, _ := strings.Cut(rt, " ")
		var body any
		if method != http.MethodGet {
			body = map[string]any{"body": "hello", "subject": "s", "description": "d", "email": "g@example.test", "name": "G",
				"category_id": h.catID.String(), "tracking_number": string(tk.TrackingNumber)}
		}
		res := h.doGuest(t, method, pattern, token, body)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		rules.scanBody(t, rt, b)
	}
}

// MCP: every tool that can return a ticket, called as a requester. The set is
// the one mcp's own test insists is classified.
func TestRequesterResponses_MCP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := everyFieldTicket(t, h)
	rules := requesterRules()
	id := tk.ID.String()

	calls := []struct {
		tool string
		args map[string]any
	}{
		{"get_ticket", map[string]any{"id": id}},
		{"list_tickets", map[string]any{}},
		{"create_ticket", map[string]any{"subject": "x", "category_id": h.catID.String()}},
		{"assign_ticket", map[string]any{"ticket_id": id, "assignee_user_id": uuid.NewString()}},
		{"update_ticket_status", map[string]any{"ticket_id": id, "status_id": uuid.NewString()}},
		{"create_follow_up", map[string]any{"ticket_id": id}},
	}
	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.userKey)
			sawTicket := false
			for _, tc := range calls {
				raw := c.call(t, tc.tool, tc.args)
				var rpc struct {
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if json.Unmarshal([]byte(strings.TrimSpace(raw)), &rpc) != nil {
					continue
				}
				for _, part := range rpc.Result.Content {
					if rules.scanBody(t, tc.tool, []byte(part.Text)) {
						sawTicket = true
					}
					// A refusal's text is a sentence, not JSON; scan it as text too.
					for k := range rules.never {
						require.NotContains(t, part.Text, `"`+k+`"`, "%s", tc.tool)
					}
				}
			}
			require.True(t, sawTicket, "no MCP tool returned a ticket to the requester, so nothing was checked")
		})
	}
}
