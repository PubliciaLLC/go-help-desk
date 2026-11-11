package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// What the one-query handler adds to GET /tickets. The tests that pin what
// existing callers see are in handler_tickets_filters_test.go.

// Free-text requester search is the one parameter that needs a scope on a
// machine credential: requester_search:read, held on purpose, implied by
// nothing. A browser session needs none. reporter_id, which names an account
// the caller already has the id of, never needs it.
func TestListTickets_RequesterSearchNeedsTheScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	h.allowStaffKeys(t)
	staffSession := h.session(t, asStaff)

	withScope := h.mintMine(t, staffSession, "tickets:read", "requester_search:read")
	withoutScope := h.mintMine(t, staffSession, "tickets:read")
	// tickets:write implies tickets:read; it must not imply the search scope.
	writeOnly := h.mintMine(t, staffSession, "tickets:write")
	adminWith, _ := h.mintAdminKey(t, "tickets:read", "requester_search:read")
	adminWithout, _ := h.mintAdminKey(t, "tickets:read")
	clientWith := h.newServiceClient(t, "rs-with", "tickets:read", "requester_search:read")
	clientWithout := h.newServiceClient(t, "rs-without", "tickets:read")

	const missing = "this credential does not carry the requester_search:read scope"
	path := "/api/v1/tickets?requester=Reporting"
	get := func(who string) listResult {
		switch who {
		case "staff session":
			return readList(t, h.doSession(t, staffSession, http.MethodGet, path, nil))
		case "admin session":
			return readList(t, h.doSession(t, h.session(t, asAdmin), http.MethodGet, path, nil))
		case "requester session":
			return readList(t, h.doSession(t, h.session(t, asReporter), http.MethodGet, path, nil))
		case "key with":
			return readList(t, withKey(t, h, withScope.Token, http.MethodGet, path, nil))
		case "key without":
			return readList(t, withKey(t, h, withoutScope.Token, http.MethodGet, path, nil))
		case "key with write only":
			return readList(t, withKey(t, h, writeOnly.Token, http.MethodGet, path, nil))
		case "admin key with":
			return readList(t, withKey(t, h, adminWith, http.MethodGet, path, nil))
		case "admin key without":
			return readList(t, withKey(t, h, adminWithout, http.MethodGet, path, nil))
		case "client with":
			return readList(t, h.doBearer(t, clientWith.Token, http.MethodGet, path, nil))
		}
		return readList(t, h.doBearer(t, clientWithout.Token, http.MethodGet, path, nil))
	}

	for _, tc := range []struct {
		who     string
		status  int
		code    string
		message string
	}{
		{"staff session", 200, "", ""},
		{"admin session", 200, "", ""},
		{"key with", 200, "", ""},
		{"admin key with", 200, "", ""},
		{"client with", 200, "", ""},
		{"key without", 403, "insufficient_scope", missing},
		{"key with write only", 403, "insufficient_scope", missing},
		{"admin key without", 403, "insufficient_scope", missing},
		{"client without", 403, "insufficient_scope", missing},
		// A requester is always themselves: not available, scope or no scope.
		{"requester session", 403, "forbidden", "requester is not available to requesters"},
	} {
		t.Run(tc.who, func(t *testing.T) {
			got := get(tc.who)
			require.Equal(t, tc.status, got.status, "body: %s", got.raw)
			if tc.status == 200 {
				// The reporting user's tickets, whatever queue the caller is on:
				// a requester search is never narrowed to "mine".
				require.Equal(t, fx.subjectsOf("abcd"), sorted(got.subjects()))
				return
			}
			require.Equal(t, tc.code, got.code)
			require.Equal(t, tc.message, got.message)
		})
	}

	t.Run("reporter_id needs no scope", func(t *testing.T) {
		p := "/api/v1/tickets?reporter_id=" + h.userID.String()
		got := readList(t, withKey(t, h, withoutScope.Token, http.MethodGet, p, nil))
		require.Equal(t, 200, got.status, "body: %s", got.raw)
		require.Equal(t, fx.subjectsOf("abcd"), sorted(got.subjects()))
		got = readList(t, h.doBearer(t, clientWithout.Token, http.MethodGet, p, nil))
		require.Equal(t, 200, got.status, "body: %s", got.raw)
	})
}

// A staff member may now filter by reporter (it was administrators only), and
// what they get is limited to what they can see.
func TestListTickets_StaffMayFilterByReporterWithinVisibility(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	got := h.listAs(t, "staff", "?reporter_id="+h.userID.String())
	require.Equal(t, 200, got.status, "body: %s", got.raw)
	require.Equal(t, fx.subjectsOf("abcd"), sorted(got.subjects()))

	// With enforcement on, the reporter's tickets the staff member could not
	// otherwise see stay hidden: only a and b (assigned to them and their group).
	enableScope(t, h)
	got = h.listAs(t, "staff", "?reporter_id="+h.userID.String())
	require.Equal(t, 200, got.status, "body: %s", got.raw)
	require.Equal(t, fx.subjectsOf("ab"), sorted(got.subjects()))

	// Their own reports, which scope always admits.
	got = h.listAs(t, "staff", "?reporter_id="+h.staffID.String())
	require.Equal(t, fx.subjectsOf("f"), sorted(got.subjects()))
}

// reporter_id with assignee_group_id used to drop the group. Both apply now.
func TestListTickets_ReporterAndGroupBothApply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	got := h.listAs(t, "admin", fmt.Sprintf("?reporter_id=%s&assignee_group_id=%s", h.userID, fx.other))
	require.Equal(t, 200, got.status, "body: %s", got.raw)
	require.Equal(t, fx.subjectsOf("c"), sorted(got.subjects()))
}

func TestListTickets_FiltersThroughTheHandler(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	for letter, priority := range map[string]string{"a": "high", "b": "high", "e": "low"} {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+fx.byName[letter].ID, map[string]any{"priority": priority})
		require.Equal(t, 200, res.StatusCode, "body: %s", readAll(t, res))
	}
	// The instant ticket e was filed and the status they all start in.
	var e struct {
		CreatedAt string `json:"created_at"`
		StatusID  string `json:"status_id"`
	}
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+fx.byName["e"].ID, nil)
	require.NoError(t, json.Unmarshal(readAll(t, res), &e))

	runListCases(t, h, fx, []listCase{
		{name: "priority", as: "admin", query: "?scope=all&priority=high", want: "ab"},
		{name: "priority repeats are ORed", as: "admin", query: "?scope=all&priority=high&priority=low", want: "abe"},
		{name: "filters are ANDed", as: "admin", query: "?scope=all&priority=high&q=alpaca&reporter_id={user}", want: "ab"},
		{name: "filters are ANDed, nothing left", as: "admin", query: "?scope=all&priority=low&q=alpaca", want: ""},
		{name: "created_before is exclusive", as: "admin", query: "?scope=all&created_before=" + url.QueryEscape(e.CreatedAt), want: "abcd"},
		{name: "created_after is inclusive", as: "admin", query: "?scope=all&created_after=" + url.QueryEscape(e.CreatedAt), want: "efgh"},
		{name: "status", as: "admin", query: "?scope=all&status_id=" + e.StatusID, want: "abcdefgh"},
		{name: "a status nothing is in", as: "admin", query: "?scope=all&status_id=" + uuid.NewString(), want: ""},
		{name: "include_closed=false keeps open tickets", as: "admin", query: "?scope=all&include_closed=false", want: "abcdefgh"},
		{name: "a category", as: "admin", query: "?scope=all&category_id={cat}", want: "abcdefgh"},
		{name: "a category nothing is in", as: "admin", query: "?scope=all&category_id=" + uuid.NewString(), want: ""},
		{name: "an assignee", as: "admin", query: "?scope=all&assignee_user_id={staff}", want: "ae"},
		{name: "source channel", as: "admin", query: "?scope=all&source_channel=api", want: "abcdefgh"},
		{name: "a source channel nobody used", as: "admin", query: "?scope=all&source_channel=phone", want: ""},
		{name: "requester text on the default queue is every queue", as: "admin", query: "?requester=Reporting", want: "abcd"},
		{name: "requester text, any case", as: "admin", query: "?requester=reporting+USER", want: "abcd"},
		{name: "requester text matches an email", as: "admin", query: "?requester=user@test", want: "abcd"},
		{name: "requester text that matches nobody", as: "admin", query: "?requester=nobody-at-all", want: ""},
		// Unknown parameters are ignored, include_internal among them.
		{name: "include_internal is ignored", as: "user", query: "?include_internal=true", want: "abcd"},
		{name: "an unknown parameter is ignored", as: "admin", query: "?scope=all&nonsense=1", want: "abcdefgh"},
	})
}

func TestListTickets_SortThroughTheHandler(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	order := func(letters string) []string {
		out := make([]string, 0, len(letters))
		for _, l := range strings.Split(letters, "") {
			out = append(out, fx.byName[l].ID)
		}
		return out
	}
	for _, tc := range []struct {
		name, query, want string
	}{
		{"default is newest first", "?scope=all", "hgfedcba"},
		{"created asc", "?scope=all&sort=created&order=asc", "abcdefgh"},
		// Subjects are all different, so there is no tie to break.
		{"subject asc", "?scope=all&sort=subject", "abcdefgh"},
		{"subject desc", "?scope=all&sort=subject&order=desc", "hgfedcba"},
		{"number asc", "?scope=all&sort=number", "abcdefgh"},
		{"number desc", "?scope=all&sort=number&order=desc", "hgfedcba"},
		{"updated is newest first by default", "?scope=all&sort=updated", "hgfedcba"},
		{"order without sort is ignored", "?scope=all&order=asc", "hgfedcba"},
		{"a sort replaces relevance", "?scope=all&q=alpaca&sort=number&order=desc", "dcba"},
		{"text alone is relevance, ties newest first", "?scope=all&q=alpaca", "dcba"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.listAs(t, "admin", tc.query)
			require.Equal(t, 200, got.status, "body: %s", got.raw)
			require.Equal(t, order(tc.want), got.ids())
		})
	}
}

// Validation runs before any query, so a refused request costs the database
// nothing, and the answers are the ones the design table gives.
func TestListTickets_Validation(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	many := func(key string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "&%s=%s", key, uuid.NewString())
		}
		return "?scope=all" + b.String()
	}
	runListCases(t, h, fx, []listCase{
		{name: "not a uuid", as: "admin", query: "?category_id=x", status: 400, code: "bad_request", message: "invalid category_id"},
		{name: "not a uuid, repeated key", as: "admin", query: "?status_id=x", status: 400, code: "bad_request", message: "invalid status_id"},
		{name: "unknown priority", as: "admin", query: "?priority=urgent", status: 400, code: "bad_request", message: "invalid priority"},
		{name: "unknown channel", as: "admin", query: "?source_channel=fax", status: 400, code: "bad_request", message: "invalid source_channel"},
		{name: "unparseable instant", as: "admin", query: "?created_after=yesterday", status: 400, code: "bad_request", message: "invalid created_after"},
		{name: "bad bool", as: "admin", query: "?include_closed=maybe", status: 400, code: "bad_request", message: "invalid include_closed"},
		{name: "inverted created range", as: "admin", query: "?created_after=2026-02-01T00:00:00Z&created_before=2026-01-01T00:00:00Z",
			status: 400, code: "bad_request", message: "created_after must be before created_before"},
		{name: "inverted updated range", as: "admin", query: "?updated_after=2026-02-01T00:00:00Z&updated_before=2026-01-01T00:00:00Z",
			status: 400, code: "bad_request", message: "updated_after must be before updated_before"},
		{name: "twenty values are fine", as: "admin", query: many("status_id", 20), want: ""},
		{name: "twenty-one are not", as: "admin", query: many("status_id", 21), status: 400, code: "bad_request", message: "too many status_id values (max 20)"},
		{name: "requester too short", as: "admin", query: "?requester=a", status: 400, code: "bad_request", message: "requester must be 2 to 254 characters"},
		{name: "requester too long", as: "admin", query: "?requester=" + strings.Repeat("a", 255), status: 400, code: "bad_request", message: "requester must be 2 to 254 characters"},
		{name: "unknown sort", as: "admin", query: "?sort=colour", status: 400, code: "bad_request", message: "invalid sort"},
		{name: "relevance is not a sort", as: "admin", query: "?sort=relevance", status: 400, code: "bad_request", message: "invalid sort"},
		{name: "unknown order", as: "admin", query: "?order=sideways", status: 400, code: "bad_request", message: "invalid order"},
		{name: "unknown order without a sort", as: "admin", query: "?scope=all&order=up", status: 400, code: "bad_request", message: "invalid order"},
		// SLA tracking is off by default.
		{name: "sla filter while tracking is off", as: "admin", query: "?sla=breached", status: 400, code: "bad_request", message: "SLA tracking is off"},
		{name: "sla sort while tracking is off", as: "admin", query: "?sort=sla", status: 400, code: "bad_request", message: "SLA tracking is off"},
		{name: "an unknown sla value is invalid first", as: "admin", query: "?sla=late", status: 400, code: "bad_request", message: "invalid sla"},
		// Requesters may filter and sort on what they are sent, and no more.
		{name: "requester: updated range", as: "user", query: "?updated_after=2026-01-01T00:00:00Z", status: 403, code: "forbidden", message: "updated_after is not available to requesters"},
		{name: "requester: assignee", as: "user", query: "?assignee_user_id={admin}", status: 403, code: "forbidden", message: "assignee_user_id is not available to requesters"},
		{name: "requester: channel", as: "user", query: "?source_channel=web", status: 403, code: "forbidden", message: "source_channel is not available to requesters"},
		{name: "requester: sla", as: "user", query: "?sla=breached", status: 403, code: "forbidden", message: "sla is not available to requesters"},
		{name: "requester: sort by updated", as: "user", query: "?sort=updated", status: 403, code: "forbidden", message: "sort by updated is not available to requesters"},
		{name: "requester: sort by sla", as: "user", query: "?sort=sla", status: 403, code: "forbidden", message: "sort by sla is not available to requesters"},
		{name: "requester: priority", as: "user", query: "?priority=medium", want: "abcd"},
		{name: "requester: created range", as: "user", query: "?created_after=2000-01-01T00:00:00Z", want: "abcd"},
		{name: "requester: sort by subject", as: "user", query: "?sort=subject", want: "abcd"},
		{name: "a malformed filter is a 400 for a requester too", as: "user", query: "?priority=urgent", status: 400, code: "bad_request", message: "invalid priority"},
		// An id that names nothing is an empty list, not an error.
		{name: "an id that names nothing", as: "admin", query: "?reporter_id=" + uuid.NewString(), want: ""},
	})

	t.Run("the sla filter works once tracking is on", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
		for _, q := range []string{"?scope=all&sla=breached", "?scope=all&sla=at_risk", "?scope=all&sort=sla"} {
			got := h.listAs(t, "admin", q)
			require.Equal(t, 200, got.status, "%s: %s", q, got.raw)
		}
		// Still not for a requester.
		require.Equal(t, 403, h.listAs(t, "user", "?sla=breached").status)
	})
}
