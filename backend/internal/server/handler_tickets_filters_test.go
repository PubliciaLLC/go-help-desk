package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// GET /tickets is one query for every request (advanced-search.md section 3).
// The tests in the first half of this file were written against the handler
// that had seven branches and a 1+N merge, and passed on it before it was
// replaced: they pin what existing callers see. The second half covers what
// the new parameters add.

// listedTicket is the part of a ticket row these tests read.
type listedTicket struct {
	ID             string `json:"id"`
	Subject        string `json:"subject"`
	TrackingNumber string `json:"tracking_number"`
}

type listResult struct {
	status  int
	tickets []listedTicket
	code    string
	message string
	raw     []byte
}

func (r listResult) subjects() []string {
	out := make([]string, len(r.tickets))
	for i, t := range r.tickets {
		out[i] = t.Subject
	}
	return out
}

func (r listResult) ids() []string {
	out := make([]string, len(r.tickets))
	for i, t := range r.tickets {
		out[i] = t.ID
	}
	return out
}

func readList(t *testing.T, res *http.Response) listResult {
	t.Helper()
	b := readAll(t, res)
	out := listResult{status: res.StatusCode, raw: b}
	if res.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(b, &out.tickets), "body: %s", b)
		return out
	}
	var e struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(b, &e), "body: %s", b)
	out.code, out.message = e.Error.Code, e.Error.Message
	return out
}

// listAs sends GET /tickets<query> as one of the seeded callers. Staff, admin
// and the requester are API keys holding every scope.
func (h *harness) listAs(t *testing.T, as string, query string) listResult {
	t.Helper()
	path := "/api/v1/tickets" + query
	switch as {
	case "admin":
		return readList(t, h.doAsAdmin(t, http.MethodGet, path, nil))
	case "user":
		return readList(t, h.doAsUser(t, http.MethodGet, path, nil))
	}
	return readList(t, h.do(t, http.MethodGet, path, nil))
}

// listFixture is eight tickets spread across reporters and assignees, and the
// two groups the staff member does and does not belong to.
type listFixture struct {
	mine, other uuid.UUID               // the staff member is in mine, not in other
	byName      map[string]listedTicket // keyed by the one-letter name in the subject
}

// newListFixture files:
//
//	a  alpaca  reporter user   assigned to staff
//	b  alpaca  reporter user   assigned to group mine
//	c  alpaca  reporter user   assigned to group other
//	d  alpaca  reporter user   unassigned; "zebra" in the description
//	e  llama   reporter admin  assigned to staff
//	f  llama   reporter staff  unassigned
//	g  llama   reporter admin  assigned to admin
//	h  llama   reporter admin  assigned to group other
func newListFixture(t *testing.T, h *harness) listFixture {
	t.Helper()
	ctx := context.Background()
	mine, err := h.groupSvc.Create(ctx, "Mine "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, mine.ID, h.staffID))
	other, err := h.groupSvc.Create(ctx, "Other "+uuid.NewString()[:8], "")
	require.NoError(t, err)

	fx := listFixture{mine: mine.ID, other: other.ID, byName: map[string]listedTicket{}}
	file := func(name, word, filer, description string, patch map[string]any) {
		body := map[string]any{
			"subject": word + " " + name, "description": description, "category_id": h.catID.String(),
		}
		var res *http.Response
		switch filer {
		case "user":
			res = h.doAsUser(t, http.MethodPost, "/api/v1/tickets", body)
		case "admin":
			res = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", body)
		default:
			res = h.do(t, http.MethodPost, "/api/v1/tickets", body)
		}
		raw := readAll(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", raw)
		var tk listedTicket
		require.NoError(t, json.Unmarshal(raw, &tk))
		if patch != nil {
			res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID, patch)
			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", readAll(t, res))
		}
		fx.byName[name] = tk
	}
	file("a", "alpaca", "user", "x", map[string]any{"assignee_user_id": h.staffID.String()})
	file("b", "alpaca", "user", "x", map[string]any{"assignee_group_id": mine.ID.String()})
	file("c", "alpaca", "user", "x", map[string]any{"assignee_group_id": other.ID.String()})
	file("d", "alpaca", "user", "zebra", nil)
	file("e", "llama", "admin", "x", map[string]any{"assignee_user_id": h.staffID.String()})
	file("f", "llama", "staff", "x", nil)
	file("g", "llama", "admin", "x", map[string]any{"assignee_user_id": h.adminID.String()})
	file("h", "llama", "admin", "x", map[string]any{"assignee_group_id": other.ID.String()})
	return fx
}

// subjectsOf turns the letters a case names into the subjects they were filed
// under, sorted so a case lists them in any order.
func (fx listFixture) subjectsOf(letters string) []string {
	out := []string{}
	for _, l := range strings.Split(letters, "") {
		if tk, ok := fx.byName[l]; ok {
			out = append(out, tk.Subject)
		}
	}
	slices.Sort(out)
	return out
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

type listCase struct {
	name  string
	as    string // admin, staff or user
	query string
	// want is the letters of the tickets that come back (any order), or ""
	// for none. A non-zero status expects a refusal instead.
	want    string
	status  int
	code    string
	message string
}

// groupOther and groupMine stand in for the fixture's ids in a case's query.
func (fx listFixture) expand(q string) string {
	r := strings.NewReplacer(
		"{mine}", fx.mine.String(), "{other}", fx.other.String(),
	)
	return r.Replace(q)
}

func runListCases(t *testing.T, h *harness, fx listFixture, cases []listCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.query
			if strings.Contains(q, "{user}") {
				q = strings.ReplaceAll(q, "{user}", h.userID.String())
			}
			if strings.Contains(q, "{admin}") {
				q = strings.ReplaceAll(q, "{admin}", h.adminID.String())
			}
			q = strings.NewReplacer("{staff}", h.staffID.String(), "{cat}", h.catID.String()).Replace(q)
			got := h.listAs(t, tc.as, fx.expand(q))
			if tc.status != 0 {
				require.Equal(t, tc.status, got.status, "body: %s", got.raw)
				if tc.code != "" {
					require.Equal(t, tc.code, got.code)
				}
				if tc.message != "" {
					require.Equal(t, tc.message, got.message)
				}
				return
			}
			require.Equal(t, http.StatusOK, got.status, "body: %s", got.raw)
			require.Equal(t, fx.subjectsOf(tc.want), sorted(got.subjects()))
		})
	}
}

// What a caller got before the one-query handler, request by request, with the
// instance's scope enforcement off (the default).
func TestListTickets_ExistingRequestsAreUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	runListCases(t, h, fx, []listCase{
		// Admin: the default list is "mine", and scope picks another queue.
		{name: "admin default is mine", as: "admin", query: "", want: "g"},
		{name: "admin scope=mine", as: "admin", query: "?scope=mine", want: "g"},
		{name: "admin scope=all", as: "admin", query: "?scope=all", want: "abcdefgh"},
		{name: "admin scope=unassigned", as: "admin", query: "?scope=unassigned", want: "df"},
		{name: "admin scope=visible", as: "admin", query: "?scope=visible", want: "abcdefgh"},
		{name: "an unknown scope is mine", as: "admin", query: "?scope=bogus", want: "g"},

		// reporter_id and assignee_group_id override scope, and the first one
		// present used to win.
		{name: "admin reporter_id", as: "admin", query: "?reporter_id={user}", want: "abcd"},
		{name: "reporter_id wins over scope", as: "admin", query: "?reporter_id={admin}&scope=unassigned", want: "egh"},
		{name: "reporter_id with text", as: "admin", query: "?reporter_id={admin}&q=llama", want: "egh"},
		{name: "reporter_id with text that is not theirs", as: "admin", query: "?reporter_id={admin}&q=alpaca", want: ""},
		{name: "invalid reporter_id", as: "admin", query: "?reporter_id=nope", status: 400, code: "bad_request", message: "invalid reporter_id"},
		{name: "admin assignee_group_id", as: "admin", query: "?assignee_group_id={other}", want: "ch"},
		{name: "assignee_group_id with text", as: "admin", query: "?assignee_group_id={other}&q=llama", want: "h"},
		{name: "invalid assignee_group_id", as: "admin", query: "?assignee_group_id=nope", status: 400, code: "bad_request", message: "invalid assignee_group_id"},

		// Text: a number prefix, words as prefixes, the description, case.
		{name: "text across all", as: "admin", query: "?scope=all&q=alpaca", want: "abcd"},
		{name: "text is a prefix match", as: "admin", query: "?scope=all&q=alpac", want: "abcd"},
		{name: "text is case-insensitive", as: "admin", query: "?scope=all&q=ALPACA", want: "abcd"},
		{name: "text reads the description", as: "admin", query: "?scope=all&q=zebra", want: "d"},
		{name: "every word must match", as: "admin", query: "?scope=all&q=alpaca+llama", want: ""},
		{name: "punctuation alone matches nothing", as: "admin", query: "?scope=all&q=%3F%3F%3F", want: ""},
		{name: "text narrows the unassigned queue", as: "admin", query: "?scope=unassigned&q=alpaca", want: "d"},
		{name: "text narrows the mine queue", as: "admin", query: "?q=llama", want: "g"},
		{name: "text on mine that is not mine", as: "admin", query: "?q=alpaca", want: ""},
		{name: "text with surrounding blanks", as: "admin", query: "?scope=all&q=++zebra++", want: "d"},

		// Staff, enforcement off.
		{name: "staff default is mine", as: "staff", query: "", want: "abe"},
		{name: "staff scope=mine", as: "staff", query: "?scope=mine", want: "abe"},
		{name: "staff scope=visible is everything", as: "staff", query: "?scope=visible", want: "abcdefgh"},
		{name: "staff scope=visible with text", as: "staff", query: "?scope=visible&q=llama", want: "efgh"},
		{name: "staff text stays on the queue", as: "staff", query: "?q=alpaca", want: "ab"},
		{name: "staff scope=all is refused", as: "staff", query: "?scope=all", status: 403, code: "forbidden", message: "only admins can use this scope"},
		{name: "staff scope=unassigned is refused", as: "staff", query: "?scope=unassigned", status: 403, code: "forbidden", message: "only admins can use this scope"},
		{name: "staff own group", as: "staff", query: "?assignee_group_id={mine}", want: "b"},
		{name: "staff another group, enforcement off", as: "staff", query: "?assignee_group_id={other}", want: "ch"},
		{name: "a group filter wins over a refused scope", as: "staff", query: "?assignee_group_id={other}&scope=all", want: "ch"},

		// A requester.
		{name: "requester default", as: "user", query: "", want: "abcd"},
		{name: "requester text", as: "user", query: "?q=alpaca", want: "abcd"},
		{name: "requester text that is not theirs", as: "user", query: "?q=llama", want: ""},
		{name: "requester scope=visible", as: "user", query: "?scope=visible", want: "abcd"},
		{name: "requester scope=mine", as: "user", query: "?scope=mine", want: "abcd"},
		{name: "requester scope=all is refused", as: "user", query: "?scope=all", status: 403, code: "forbidden", message: "only admins can use this scope"},
		{name: "requester scope=unassigned is refused", as: "user", query: "?scope=unassigned", status: 403, code: "forbidden", message: "only admins can use this scope"},
		{name: "requester may not list a group", as: "user", query: "?assignee_group_id={mine}", status: 403, code: "forbidden", message: "users cannot list group tickets"},
		{name: "requester may not name a reporter", as: "user", query: "?reporter_id={user}", status: 403, code: "forbidden"},
	})
}

// With enforcement on, a staff member's default list is what the scope model
// admits, and the rest is as it was.
func TestListTickets_ExistingRequestsAreUnchanged_ScopeEnforced(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	enableScope(t, h)

	runListCases(t, h, fx, []listCase{
		// Reported by staff (f), assigned to them (a, e) or to their group (b).
		{name: "staff default is what they can see", as: "staff", query: "", want: "abef"},
		{name: "staff scope=mine is the same", as: "staff", query: "?scope=mine", want: "abef"},
		{name: "staff scope=visible", as: "staff", query: "?scope=visible", want: "abef"},
		{name: "staff text", as: "staff", query: "?q=llama", want: "ef"},
		{name: "staff text via scope=visible", as: "staff", query: "?scope=visible&q=alpaca", want: "ab"},
		{name: "staff own group", as: "staff", query: "?assignee_group_id={mine}", want: "b"},
		{name: "staff not in that group", as: "staff", query: "?assignee_group_id={other}", status: 403, code: "forbidden", message: "you are not a member of that group"},
		{name: "staff scope=all is refused", as: "staff", query: "?scope=all", status: 403, code: "forbidden", message: "only admins can use this scope"},
		// Administrators are not scoped.
		{name: "admin default", as: "admin", query: "", want: "g"},
		{name: "admin scope=all", as: "admin", query: "?scope=all", want: "abcdefgh"},
		{name: "admin scope=visible", as: "admin", query: "?scope=visible", want: "abcdefgh"},
		{name: "admin another group", as: "admin", query: "?assignee_group_id={other}", want: "ch"},
		{name: "requester default", as: "user", query: "", want: "abcd"},
	})
}

// A ticket number: the whole number, a prefix of it, and a bare run of digits.
func TestListTickets_TicketNumberSearchIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	all := []listedTicket{}
	for _, tk := range fx.byName {
		all = append(all, tk)
	}
	matching := func(match func(string) bool) []string {
		var out []string
		for _, tk := range all {
			if match(tk.TrackingNumber) {
				out = append(out, tk.Subject)
			}
		}
		slices.Sort(out)
		return out
	}

	one := fx.byName["c"]
	got := h.listAs(t, "admin", "?scope=all&q="+url.QueryEscape(one.TrackingNumber))
	require.Equal(t, http.StatusOK, got.status)
	require.Equal(t, []string{one.Subject}, got.subjects())

	// A prefix, with the last digit dropped: a suffix wildcard only.
	prefix := one.TrackingNumber[:len(one.TrackingNumber)-1]
	got = h.listAs(t, "admin", "?scope=all&q="+url.QueryEscape(prefix))
	require.Equal(t, matching(func(n string) bool { return strings.HasPrefix(n, prefix) }), sorted(got.subjects()))
	got = h.listAs(t, "admin", "?scope=all&q="+url.QueryEscape(strings.ToLower(prefix)))
	require.Equal(t, matching(func(n string) bool { return strings.HasPrefix(n, prefix) }), sorted(got.subjects()),
		"a prefix is matched without regard to case")

	// A bare number is a substring.
	digits := one.TrackingNumber[len(one.TrackingNumber)-2:]
	got = h.listAs(t, "admin", "?scope=all&q="+digits)
	require.Equal(t, matching(func(n string) bool { return strings.Contains(n, digits) }), sorted(got.subjects()))

	// The same number, asked by someone who may not see the ticket.
	got = h.listAs(t, "user", "?q="+url.QueryEscape(fx.byName["f"].TrackingNumber))
	require.Empty(t, got.tickets)
}

// Newest first, and a page boundary that neither repeats nor skips a ticket.
func TestListTickets_OrderAndPagingAreUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	// Filed a..h in that order, one at a time.
	order := []string{}
	for _, l := range []string{"h", "g", "f", "e", "d", "c", "b", "a"} {
		order = append(order, fx.byName[l].ID)
	}
	require.Equal(t, order, h.listAs(t, "admin", "?scope=all").ids(), "newest first")

	var paged []string
	for off := 0; off < 8; off += 3 {
		page := h.listAs(t, "admin", fmt.Sprintf("?scope=all&limit=3&offset=%d", off))
		require.Equal(t, http.StatusOK, page.status)
		paged = append(paged, page.ids()...)
	}
	require.Equal(t, order, paged)
	require.Empty(t, h.listAs(t, "admin", "?scope=all&limit=3&offset=8").tickets)
	require.Len(t, h.listAs(t, "admin", "?scope=all&limit=5&offset=6").tickets, 2)
}

// The 1+N merge the handler used to run for "mine": the caller's own tickets
// and each group's, each read up to the end of the page, deduplicated, sorted
// newest first with the id as the tiebreak, and sliced once. Rebuilt here from
// the service calls it made, so the one-query handler is held to what it
// replaced. Removing those service methods removes this reference; the
// equality it proves has by then been merged.
func oldMineMerge(t *testing.T, h *harness, q string, limit, offset int) []string {
	t.Helper()
	ctx := context.Background()
	window := offset + limit

	var all []struct {
		id      string
		created int64
	}
	seen := map[uuid.UUID]bool{}
	add := func(id uuid.UUID, created int64) {
		if !seen[id] {
			seen[id] = true
			all = append(all, struct {
				id      string
				created int64
			}{id.String(), created})
		}
	}

	if q != "" {
		mine, err := h.ticketSvc.SearchByAssigneeUser(ctx, h.staffID, q, window, 0)
		require.NoError(t, err)
		for _, tk := range mine {
			add(tk.ID, tk.CreatedAt.UnixNano())
		}
	} else {
		mine, err := h.ticketSvc.ListByAssigneeUser(ctx, h.staffID, window, 0)
		require.NoError(t, err)
		for _, tk := range mine {
			add(tk.ID, tk.CreatedAt.UnixNano())
		}
	}
	groups, err := h.groupSvc.ListGroupsForUser(ctx, h.staffID)
	require.NoError(t, err)
	for _, g := range groups {
		if q != "" {
			ts, err := h.ticketSvc.SearchByAssigneeGroup(ctx, g.ID, q, window, 0)
			require.NoError(t, err)
			for _, tk := range ts {
				add(tk.ID, tk.CreatedAt.UnixNano())
			}
		} else {
			ts, err := h.ticketSvc.ListByAssigneeGroup(ctx, g.ID, window, 0)
			require.NoError(t, err)
			for _, tk := range ts {
				add(tk.ID, tk.CreatedAt.UnixNano())
			}
		}
	}
	slices.SortFunc(all, func(x, y struct {
		id      string
		created int64
	}) int {
		if x.created != y.created {
			if x.created > y.created {
				return -1
			}
			return 1
		}
		return strings.Compare(x.id, y.id)
	})
	ids := make([]string, len(all))
	for i, a := range all {
		ids[i] = a.id
	}
	if offset >= len(ids) {
		return []string{}
	}
	return ids[offset:min(offset+limit, len(ids))]
}

// The "mine" queue, for a staff member in two groups, with tickets assigned to
// them, to either group, to both them and a group, and to neither.
func TestListTickets_MineQueueEqualsTheOldMerge(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	gA, err := h.groupSvc.Create(ctx, "A "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	gB, err := h.groupSvc.Create(ctx, "B "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	gC, err := h.groupSvc.Create(ctx, "C "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, gA.ID, h.staffID))
	require.NoError(t, h.groupSvc.AddMember(ctx, gB.ID, h.staffID))

	patches := []map[string]any{
		{"assignee_user_id": h.staffID.String()},
		{"assignee_group_id": gA.ID.String()},
		{"assignee_group_id": gB.ID.String()},
		// Both: in the user's own list and a group's, and returned once.
		{"assignee_user_id": h.staffID.String(), "assignee_group_id": gA.ID.String()},
		{"assignee_group_id": gC.ID.String()},
		{"assignee_user_id": h.adminID.String()},
		nil,
	}
	const total = 28
	for i := 0; i < total; i++ {
		word := "item"
		if i%3 == 0 {
			word = "thing"
		}
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": fmt.Sprintf("%s %02d", word, i), "description": "x", "category_id": h.catID.String(),
		})
		raw := readAll(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", raw)
		var tk listedTicket
		require.NoError(t, json.Unmarshal(raw, &tk))
		if p := patches[i%len(patches)]; p != nil {
			res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID, p)
			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", readAll(t, res))
		}
	}

	for _, limit := range []int{4, 7, 100} {
		for offset := 0; offset < 24; offset += limit {
			t.Run(fmt.Sprintf("limit=%d offset=%d", limit, offset), func(t *testing.T) {
				got := h.listAs(t, "staff", fmt.Sprintf("?limit=%d&offset=%d", limit, offset))
				require.Equal(t, http.StatusOK, got.status, "body: %s", got.raw)
				require.Equal(t, oldMineMerge(t, h, "", limit, offset), got.ids())
			})
		}
	}

	// A text search on "mine" used to come back newest first and now comes back
	// ranked (a decision: advanced-search.md open question 3), so only the set
	// is held to what it was.
	for _, q := range []string{"item", "thing"} {
		t.Run("text "+q, func(t *testing.T) {
			got := h.listAs(t, "staff", "?q="+q)
			require.Equal(t, http.StatusOK, got.status, "body: %s", got.raw)
			require.ElementsMatch(t, oldMineMerge(t, h, q, 100, 0), got.ids())
		})
	}
}
