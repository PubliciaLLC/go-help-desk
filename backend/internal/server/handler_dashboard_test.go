package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// GET /api/v1/dashboard (#675). The numbers and rows are the list's own: these
// tests hold the queue strip to GET /tickets, the attention rows to the
// visibility the list applies, and the trend to the scope the caller has.
// The fixture is newListFixture (see handler_tickets_filters_test.go):
//
//	a  assigned to staff           e  assigned to staff
//	b  group mine                  f  unassigned, reported by staff
//	c  group other                 g  assigned to admin
//	d  unassigned                  h  group other

type dashQueue struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type dashAttention struct {
	Reason string                     `json:"reason"`
	Since  time.Time                  `json:"since"`
	Ticket map[string]json.RawMessage `json:"ticket"`
}

type dashResult struct {
	status  int
	raw     []byte
	code    string
	message string
	top     map[string]json.RawMessage
	Queues  []dashQueue
}

func (d dashResult) keys() []string {
	out := []string{}
	for _, q := range d.Queues {
		out = append(out, q.Key)
	}
	return out
}

func (d dashResult) count(t *testing.T, key string) int64 {
	t.Helper()
	for _, q := range d.Queues {
		if q.Key == key {
			return q.Count
		}
	}
	t.Fatalf("no %q cell in %v", key, d.keys())
	return 0
}

func (d dashResult) attention(t *testing.T) []dashAttention {
	t.Helper()
	var out []dashAttention
	require.NoError(t, json.Unmarshal(d.top["attention"], &out), "body: %s", d.raw)
	return out
}

func (d dashResult) reasons(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, a := range d.attention(t) {
		out = append(out, a.Reason)
	}
	return out
}

func (d dashResult) attentionSubjects(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, a := range d.attention(t) {
		var s string
		require.NoError(t, json.Unmarshal(a.Ticket["subject"], &s))
		out = append(out, s)
	}
	return out
}

type dashVolume struct {
	TZ   string `json:"tz"`
	Days []struct {
		Date     string `json:"date"`
		Created  int64  `json:"created"`
		Resolved int64  `json:"resolved"`
	} `json:"days"`
}

func (d dashResult) volume(t *testing.T) dashVolume {
	t.Helper()
	var v dashVolume
	require.NoError(t, json.Unmarshal(d.top["volume"], &v), "body: %s", d.raw)
	return v
}

func (v dashVolume) created() int64 {
	var n int64
	for _, d := range v.Days {
		n += d.Created
	}
	return n
}

func (h *harness) dashboardAs(t *testing.T, as, query string) dashResult {
	t.Helper()
	path := "/api/v1/dashboard" + query
	var res *http.Response
	switch as {
	case "admin":
		res = h.doAsAdmin(t, http.MethodGet, path, nil)
	case "user":
		res = h.doAsUser(t, http.MethodGet, path, nil)
	default:
		res = h.do(t, http.MethodGet, path, nil)
	}
	return readDashboard(t, res)
}

func readDashboard(t *testing.T, res *http.Response) dashResult {
	t.Helper()
	raw := readAll(t, res)
	d := dashResult{status: res.StatusCode, raw: raw}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		d.code, d.message = e.Error.Code, e.Error.Message
		return d
	}
	require.NoError(t, json.Unmarshal(raw, &d.top), "body: %s", raw)
	require.NoError(t, json.Unmarshal(d.top["queues"], &d.Queues), "body: %s", raw)
	return d
}

// cellParams is the DESIGN.md queue-strip table: what GET /tickets is sent to
// give each cell's count.
var cellParams = map[string]string{
	"assigned_to_me":    "scope=assigned_to_me&include_closed=false",
	"my_groups":         "scope=my_groups&include_closed=false",
	"sla_breached":      "scope=assigned_to_me&sla=breached&include_closed=false",
	"sla_at_risk":       "scope=assigned_to_me&sla=at_risk&include_closed=false",
	"awaiting_my_reply": "scope=assigned_to_me&awaiting_reply=true&include_closed=false",
	"my_tickets":        "scope=mine&include_closed=false",
}

func setBackdate(t *testing.T, h *harness, id string, age time.Duration) time.Time {
	t.Helper()
	at := time.Now().Add(-age).UTC().Truncate(time.Microsecond)
	_, err := h.tx.ExecContext(context.Background(), `UPDATE tickets SET created_at = $1 WHERE id = $2`, at, id)
	require.NoError(t, err)
	return at
}

func enableSLAWithShortPolicy(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 1, 480)
}

// reopenedTicket files a ticket for the requester, resolves it and has the
// requester answer, which reopens it (the real path, so last_reopened_at is
// the server's own).
func reopenedTicket(t *testing.T, h *harness, subject string) (ticket.Ticket, time.Time) {
	t.Helper()
	ctx := context.Background()
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: subject, CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done", ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff})
	require.NoError(t, err)
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "still broken"})
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", readAll(t, res))
	var at time.Time
	require.NoError(t, h.tx.QueryRowContext(ctx, `SELECT last_reopened_at FROM tickets WHERE id = $1`, tk.ID).Scan(&at))
	return tk, at
}

func TestDashboard_KeysAndNullsPerRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	cases := []struct {
		as       string
		keys     []string
		nullRest bool
		rows     int // attention rows: an administrator is offered the unassigned d and f
	}{
		{"staff", []string{"assigned_to_me", "my_groups", "awaiting_my_reply"}, false, 0},
		{"admin", []string{"assigned_to_me", "my_groups", "awaiting_my_reply"}, false, 2},
		{"user", []string{"my_tickets"}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.as, func(t *testing.T) {
			d := h.dashboardAs(t, tc.as, "")
			require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
			require.Equal(t, tc.keys, d.keys(), "SLA is off, so no sla_* cell")
			if tc.nullRest {
				require.JSONEq(t, `null`, string(d.top["attention"]))
				require.JSONEq(t, `null`, string(d.top["volume"]))
				return
			}
			require.NotEqual(t, "null", string(d.top["attention"]), "[] is nothing to show, not null")
			require.Len(t, d.attention(t), tc.rows)
			v := d.volume(t)
			require.Equal(t, "UTC", v.TZ)
			require.Len(t, v.Days, 30)
			require.Equal(t, time.Now().UTC().Format("2006-01-02"), v.Days[29].Date)
		})
	}

	t.Run("counts", func(t *testing.T) {
		staff := h.dashboardAs(t, "staff", "")
		require.EqualValues(t, 2, staff.count(t, "assigned_to_me"))
		require.EqualValues(t, 1, staff.count(t, "my_groups"))
		require.EqualValues(t, 2, staff.count(t, "awaiting_my_reply"))
		require.EqualValues(t, 4, h.dashboardAs(t, "user", "").count(t, "my_tickets"))
	})
}

// A cell's number is the length of the list its link opens, for every role,
// with enforcement off and on and SLA tracking on.
func TestDashboard_EachCountEqualsTheList(t *testing.T) {
	for _, enforced := range []bool{false, true} {
		name := "enforcement off"
		if enforced {
			name = "enforcement on"
		}
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			enableSLAWithShortPolicy(t, h)
			fx := newListFixture(t, h)
			setBackdate(t, h, fx.byName["a"].ID, 72*time.Hour)
			setBackdate(t, h, fx.byName["e"].ID, 48*time.Hour)
			// One closed, so include_closed=false has something to leave out.
			closeID := uuid.MustParse(fx.byName["g"].ID)
			require.NoError(t, h.ticketSvc.Close(context.Background(), closeID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
			reopenedTicket(t, h, "answered again")
			if enforced {
				enableScope(t, h)
			}

			for _, as := range []string{"admin", "staff", "user"} {
				d := h.dashboardAs(t, as, "")
				require.Equal(t, http.StatusOK, d.status, "%s: %s", as, d.raw)
				require.NotEmpty(t, d.Queues)
				for _, q := range d.Queues {
					list := h.listAs(t, as, "?"+cellParams[q.Key])
					require.Equal(t, http.StatusOK, list.status, "%s %s: %s", as, q.Key, list.raw)
					require.EqualValues(t, len(list.tickets), q.Count, "%s %s", as, q.Key)
				}
			}
			staff := h.dashboardAs(t, "staff", "")
			require.Equal(t, []string{"assigned_to_me", "my_groups", "sla_breached", "sla_at_risk", "awaiting_my_reply"}, staff.keys())
			require.EqualValues(t, 2, staff.count(t, "sla_breached"), "a and e are past a one-minute target")
		})
	}
}

func TestDashboard_ScopeEnforcementNarrowsEverything(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	setAvailableRoles(t, h, `["staff"]`)
	// Everything unassigned is old enough to be told apart: d older than f.
	setBackdate(t, h, fx.byName["d"].ID, 5*24*time.Hour)
	setBackdate(t, h, fx.byName["f"].ID, 4*24*time.Hour)

	wide := h.dashboardAs(t, "staff", "")
	require.Equal(t, http.StatusOK, wide.status, "body: %s", wide.raw)
	require.Equal(t, []string{"unassigned", "unassigned"}, wide.reasons(t))
	require.Equal(t, []string{"alpaca d", "llama f"}, wide.attentionSubjects(t), "oldest first")
	require.EqualValues(t, 8, wide.volume(t).created())

	enableScope(t, h)
	// The staff member's group covers no category: what they reported, what is
	// assigned to them and what is assigned to their group.
	narrow := h.dashboardAs(t, "staff", "")
	require.Equal(t, http.StatusOK, narrow.status, "body: %s", narrow.raw)
	require.Equal(t, []string{"llama f"}, narrow.attentionSubjects(t), "d is outside the scope")
	require.EqualValues(t, 4, narrow.volume(t).created(), "a, b, e and f")

	// A category the group covers brings d back, as it does for Available.
	require.NoError(t, h.groupSvc.AddScope(context.Background(), group.GroupScope{GroupID: fx.mine, CategoryID: h.catID}))
	covered := h.dashboardAs(t, "staff", "")
	require.Equal(t, []string{"alpaca d", "llama f"}, covered.attentionSubjects(t))
	require.EqualValues(t, 8, covered.volume(t).created())

	t.Run("an administrator is never scoped", func(t *testing.T) {
		d := h.dashboardAs(t, "admin", "")
		require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
		require.EqualValues(t, 8, d.volume(t).created())
		require.Equal(t, []string{"alpaca d", "llama f"}, d.attentionSubjects(t))
	})
}

func TestDashboard_UnassignedFollowsTheAvailableSetting(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		as     string
		want   []string
	}{
		{"staff, not ticked", `["admin"]`, "staff", []string{}},
		{"staff, key absent", ``, "staff", []string{}},
		{"staff, ticked", `["staff"]`, "staff", []string{"unassigned", "unassigned"}},
		{"admin, not ticked", `[]`, "admin", []string{"unassigned", "unassigned"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			newListFixture(t, h)
			if tc.stored != "" {
				setAvailableRoles(t, h, tc.stored)
			}
			d := h.dashboardAs(t, tc.as, "")
			require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
			require.Equal(t, tc.want, d.reasons(t))
		})
	}
}

func TestDashboard_SLAOffHasNoSLACellAndNoSLAReason(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	// A policy exists and a ticket is long past it, but tracking is off.
	createCatchAllSLAPolicy(t, h, 1, 480)
	id := createTicketForSLA(t, h, "ancient")
	setBackdate(t, h, id, 10*24*time.Hour)

	for _, as := range []string{"staff", "admin"} {
		d := h.dashboardAs(t, as, "")
		require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
		require.Equal(t, []string{"assigned_to_me", "my_groups", "awaiting_my_reply"}, d.keys(), as)
		require.NotContains(t, d.reasons(t), "breached")
		require.NotContains(t, d.reasons(t), "at_risk")
	}

	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	d := h.dashboardAs(t, "admin", "")
	require.Equal(t, []string{"assigned_to_me", "my_groups", "sla_breached", "sla_at_risk", "awaiting_my_reply"}, d.keys())
}

func TestDashboard_AttentionRowsAndSince(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableSLAWithShortPolicy(t, h)

	// Two breached, oldest first. Both are unassigned too, so they are the
	// dedupe case for an administrator: listed once, as breached.
	older := createTicketForSLA(t, h, "breached older")
	newer := createTicketForSLA(t, h, "breached newer")
	olderAt := setBackdate(t, h, older, 3*24*time.Hour)
	newerAt := setBackdate(t, h, newer, 2*24*time.Hour)
	// A reopen restarts the SLA record, so this one is not breached. It is
	// unassigned (which an administrator is shown) and reopened (which staff,
	// not ticked for Available, are shown).
	back, reopenedAt := reopenedTicket(t, h, "back again")
	backAt := setBackdate(t, h, back.ID.String(), 24*time.Hour)

	cases := []struct {
		as      string
		reasons []string
		since   []time.Time
	}{
		{"staff", []string{"breached", "breached", "reopened"}, []time.Time{olderAt, newerAt, reopenedAt}},
		{"admin", []string{"breached", "breached", "unassigned"}, []time.Time{olderAt, newerAt, backAt}},
	}
	for _, tc := range cases {
		t.Run(tc.as, func(t *testing.T) {
			d := h.dashboardAs(t, tc.as, "")
			require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
			rows := d.attention(t)
			require.Equal(t, tc.reasons, d.reasons(t), "each ticket once, under its first reason")
			require.Equal(t, []string{"breached older", "breached newer", "back again"}, d.attentionSubjects(t))
			for i, r := range rows {
				require.True(t, tc.since[i].Equal(r.Since), "row %d since %v, want %v", i, r.Since, tc.since[i])
			}

			// The ticket is the object GET /tickets sends this caller.
			list := h.listAs(t, tc.as, "?scope=visible&include_closed=false")
			require.Equal(t, http.StatusOK, list.status)
			var listed []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(list.raw, &listed))
			byID := map[string]map[string]json.RawMessage{}
			for _, l := range listed {
				var id string
				require.NoError(t, json.Unmarshal(l["id"], &id))
				byID[id] = l
			}
			for _, r := range rows {
				var id string
				require.NoError(t, json.Unmarshal(r.Ticket["id"], &id))
				require.Contains(t, byID, id)
				require.Equal(t, rawKeys(byID[id]), rawKeys(r.Ticket), "the same object the list sends")
				require.Contains(t, r.Ticket, "sla")
				require.JSONEq(t, string(byID[id]["subject"]), string(r.Ticket["subject"]))
			}
		})
	}
}

func rawKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A reopened row is measured from the reopen, which is not the ticket's
// created_at.
func TestDashboard_ReopenedRowSinceIsTheReopenInstant(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, reopenedAt := reopenedTicket(t, h, "back again")
	created := setBackdate(t, h, tk.ID.String(), 20*24*time.Hour)
	// Outside the seven days: not listed.
	stale, _ := reopenedTicket(t, h, "reopened long ago")
	_, err := h.tx.ExecContext(context.Background(),
		`UPDATE tickets SET last_reopened_at = now() - interval '8 days' WHERE id = $1`, stale.ID)
	require.NoError(t, err)

	d := h.dashboardAs(t, "staff", "")
	require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
	rows := d.attention(t)
	require.Len(t, rows, 1, "body: %s", d.raw)
	require.Equal(t, "reopened", rows[0].Reason)
	require.True(t, reopenedAt.Equal(rows[0].Since), "since %v, reopened at %v", rows[0].Since, reopenedAt)
	require.False(t, created.Equal(rows[0].Since))
	// The reopen instant is sent on the row and not on the ticket.
	require.NotContains(t, rows[0].Ticket, "last_reopened_at")
}

func TestDashboard_ResolvedAndClosedAreNotAttention(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableSLAWithShortPolicy(t, h)
	ctx := context.Background()

	open := createTicketForSLA(t, h, "open and late")
	resolved := createTicketForSLA(t, h, "resolved and late")
	closed := createTicketForSLA(t, h, "closed and late")
	for _, id := range []string{open, resolved, closed} {
		setBackdate(t, h, id, 3*24*time.Hour)
	}
	staff := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	_, err := h.ticketSvc.Resolve(ctx, uuid.MustParse(resolved), "done", staff)
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, uuid.MustParse(closed), ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))

	d := h.dashboardAs(t, "admin", "")
	require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
	require.Equal(t, []string{"open and late"}, d.attentionSubjects(t))
}

func TestDashboard_AttentionStopsAtFive(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enableSLAWithShortPolicy(t, h)
	for i := 0; i < 7; i++ {
		id := createTicketForSLA(t, h, "late "+string(rune('a'+i)))
		setBackdate(t, h, id, time.Duration(10-i)*24*time.Hour)
	}
	d := h.dashboardAs(t, "admin", "")
	require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
	require.Equal(t, []string{"late a", "late b", "late c", "late d", "late e"}, d.attentionSubjects(t))
}

func TestDashboard_TZ(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	long := strings.Repeat("a", 65)
	cases := []struct {
		name    string
		query   string
		as      string
		status  int
		message string
		tz      string
	}{
		{name: "absent is UTC", query: "", as: "staff", status: 200, tz: "UTC"},
		{name: "empty is UTC", query: "?tz=", as: "staff", status: 200, tz: "UTC"},
		{name: "a zone", query: "?tz=Europe/London", as: "staff", status: 200, tz: "Europe/London"},
		{name: "a zone, escaped slash", query: "?tz=America%2FNew_York", as: "admin", status: 200, tz: "America/New_York"},
		{name: "an offset-style name", query: "?tz=Etc/GMT%2B5", as: "admin", status: 200, tz: "Etc/GMT+5"},
		{name: "65 bytes is too long", query: "?tz=" + long, as: "staff", status: 400, message: "invalid tz"},
		{name: "a space", query: "?tz=" + url.QueryEscape("Europe London"), as: "staff", status: 400, message: "invalid tz"},
		{name: "a quote", query: "?tz=" + url.QueryEscape("a'b"), as: "staff", status: 400, message: "invalid tz"},
		{name: "a semicolon", query: "?tz=" + url.QueryEscape("UTC;select 1"), as: "staff", status: 400, message: "invalid tz"},
		{name: "a non-ASCII letter", query: "?tz=" + url.QueryEscape("Zürich"), as: "staff", status: 400, message: "invalid tz"},
		{name: "a backslash", query: "?tz=" + url.QueryEscape(`a\b`), as: "staff", status: 400, message: "invalid tz"},
		{name: "bad characters are refused to a requester too", query: "?tz=" + url.QueryEscape("a b"), as: "user", status: 400, message: "invalid tz"},
		{name: "a requester runs no trend, so an unknown zone is not looked up", query: "?tz=Mars/Olympus_Mons", as: "user", status: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := h.dashboardAs(t, tc.as, tc.query)
			require.Equal(t, tc.status, d.status, "body: %s", d.raw)
			if tc.status != 200 {
				require.Equal(t, "bad_request", d.code)
				require.Equal(t, tc.message, d.message)
				return
			}
			if tc.tz != "" {
				v := d.volume(t)
				require.Equal(t, tc.tz, v.TZ)
				require.Len(t, v.Days, 30)
			}
		})
	}
}

// Postgres refuses the name inside the harness transaction, which a failed
// statement leaves dead, so each unknown name has a harness of its own.
func TestDashboard_TZUnknownToPostgres(t *testing.T) {
	for _, tz := range []string{
		"Mars/Olympus_Mons",
		strings.Repeat("a", 64), // the longest the syntax check lets through
		"Not_A_Zone",
	} {
		t.Run(tz, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			d := h.dashboardAs(t, "staff", "?tz="+url.QueryEscape(tz))
			require.Equal(t, http.StatusBadRequest, d.status, "body: %s", d.raw)
			require.Equal(t, "bad_request", d.code)
			require.Equal(t, "unknown time zone", d.message)
		})
	}
}

func TestDashboard_NeedsTicketsRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("a key without tickets:read", func(t *testing.T) {
		key := mintKey(t, h, []string{"users:read"})
		res := withKey(t, h, key, http.MethodGet, "/api/v1/dashboard", nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", readAll(t, res))
	})
	t.Run("a key with tickets:read", func(t *testing.T) {
		key := mintKey(t, h, []string{"tickets:read"})
		d := readDashboard(t, withKey(t, h, key, http.MethodGet, "/api/v1/dashboard", nil))
		require.Equal(t, http.StatusOK, d.status, "body: %s", d.raw)
		require.Contains(t, d.keys(), "assigned_to_me")
	})
	t.Run("no credential", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
