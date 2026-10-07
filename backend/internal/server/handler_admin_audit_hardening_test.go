package server_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Findings from the pre-merge review of #328. Each test here exists because
// mutating the thing it describes left every pre-existing test green.

// plantAuditEntry writes an entry directly, so a test can assert on a payload
// nothing in the application writes today.
//
// That is the point. audit.Redact is documented as a denylist "for the shape
// of the problem, not a value observed in this codebase" — which means its
// only enforcement point was never exercised end to end, and removing the
// Redact call from either handler left the suite green.
func plantAuditEntry(t *testing.T, h *harness, entityType string, entityID uuid.UUID, after map[string]any) {
	t.Helper()
	require.NoError(t, h.auditStore.Create(context.Background(), audit.Entry{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   entityID,
		Action:     "fixture_sensitive",
		After:      after,
		CreatedAt:  time.Now(),
	}))
}

// A sensitive value never reaches the wire, through either reader.
//
// Two call sites behind one function is exactly where one gets missed, and
// before this nothing would have noticed: deleting the audit.Redact call from
// handler_admin_audit.go, or from handler_tickets.go, broke no test.
func TestAudit_RedactsSensitiveValuesThroughBothReaders(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	ticketID := createAndResolveTicket(t, h)
	tid := uuid.MustParse(ticketID)
	secret := "hunter2-this-must-never-render"
	// Both maps. The first version planted only After, so mutating Redact to
	// `return before, redactMap(after)` left the test green — half the
	// function was unguarded by the test written to guard it.
	plantAuditEntry(t, h, "ticket", tid, map[string]any{
		"password_hash": secret,
		"mfa_secret":    secret,
		"subject":       "this one is not sensitive and must survive",
	})
	plantAuditEntryBefore(t, h, "ticket", tid, map[string]any{
		"password_hash": secret,
		"token":         secret,
		"subject":       "before-side value that is not sensitive",
	})

	for _, route := range []string{
		"/api/v1/admin/audit?entity_type=ticket",
		"/api/v1/tickets/" + ticketID + "/audit",
	} {
		t.Run(route, func(t *testing.T) {
			resp := h.doAsAdmin(t, http.MethodGet, route, nil)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			raw, err := readAllBody(resp)
			require.NoError(t, err)

			// The blunt assertion first: the value is not anywhere in the
			// response, whatever the shape of the JSON around it.
			require.NotContains(t, raw, secret, "a sensitive value reached the wire")
			require.Contains(t, raw, "[redacted]",
				"the field was dropped rather than redacted, which hides that it changed")
			require.Contains(t, raw, "this one is not sensitive and must survive",
				"redaction removed a field it should have left alone")
		})
	}
}

// Staff are told how many entries they can see, and nothing about the ones
// they cannot.
//
// `total` used to be Search's own count, taken before ticket scope was
// applied. Entries were correctly withheld; the number was not — and with
// actor_id, action and from/to filters available, a count is an oracle: ask
// for a filter, read the number, bisect the time window, and date activity on
// tickets you have no scope over. The count now comes from the same predicate
// as the page, so it is a count of what the caller can see.
func TestAdminAudit_StaffTotalCountsOnlyWhatTheySee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Five tickets the staff member cannot see, all created by the admin, and
	// one they can.
	for i := 0; i < 5; i++ {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
		})
		resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}
	mine := createAndResolveTicket(t, h)
	enableScope(t, h)

	total := func(do func(*testing.T, string, string, any) *http.Response, query string) (n int, entries int) {
		resp := do(t, http.MethodGet, "/api/v1/admin/audit?"+query, nil)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			Total   *int  `json:"total"`
			Entries []any `json:"entries"`
		}
		decodeJSON(t, resp, &body)
		require.NotNil(t, body.Total, "staff were given no count at all")
		return *body.Total, len(body.Entries)
	}

	// The oracle itself: entries created by the admin, on tickets staff cannot
	// see. The admin's own count is what a leak would reveal.
	query := "action=created&actor_id=" + h.adminID.String()
	adminSees, _ := total(h.doAsAdmin, query)
	require.GreaterOrEqual(t, adminSees, 5, "precondition: those entries exist")
	n, shown := total(h.do, query)
	require.Zero(t, n, "staff were told how many entries exist on tickets they may not see")
	require.Zero(t, shown)

	// And what they can see is counted exactly.
	want := adminAuditIDsFor(t, h, []string{mine})
	require.NotEmpty(t, want)
	n, shown = total(h.do, "")
	require.Equal(t, len(want), n)
	require.Equal(t, len(want), shown)
}

// An admin still gets one, because there is nothing being withheld from them
// for it to leak.
func TestAdminAudit_AdminStillGetsACount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createAndResolveTicket(t, h)

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit", nil)
	defer resp.Body.Close()
	var body struct {
		Total *int `json:"total"`
	}
	decodeJSON(t, resp, &body)
	require.NotNil(t, body.Total, "the admin count was removed along with the staff one")
	require.Greater(t, *body.Total, 0)
}

// Consecutive pages neither repeat nor skip an entry, with out-of-scope
// entries interleaved and scope actually enforced.
//
// The first version of this test created every ticket as the staff user and
// never called enableScope, so nothing was out of scope and it proved nothing
// about scope. It also only checked for duplicates, so a fix that silently
// SKIPPED entries would have passed. The assertion is equality against the full
// expected sequence, not an absence of repeats, and the total is checked on
// every page.
//
// The defect it was written for: with scope applied in Go after the query, the
// offset indexed the raw rows while the entries were the scope-filtered subset,
// so advancing a page skipped a different number of rows than the caller had
// been shown, and consecutive pages overlapped. Scope is in the query now, so
// offset and page are one sequence.
func TestAdminAudit_StaffPagingMatchesTheVisibleSequence(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Interleaved: staff ticket, admin ticket, staff ticket, ... so that any
	// confusion between the raw and the visible sequence shows up.
	var mine []string
	for i := 0; i < 5; i++ {
		mine = append(mine, createAndResolveTicket(t, h)) // staff session
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
		})
		resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	enableScope(t, h)

	// What the staff member should see, in order — derived from the ADMIN
	// view, which applies no scope in its query, filtered
	// to the staff member's own tickets. The first version read this from the
	// staff endpoint itself, so a defect the endpoint made consistently — say,
	// never emitting the oldest visible entry — appeared in both `want` and
	// `got`, and the test agreed with itself while both were wrong.
	want := adminAuditIDsFor(t, h, mine)
	require.NotEmpty(t, want, "fixture produced nothing visible to staff")

	require.Equal(t, want, staffAuditIDs(t, h, 500, 0),
		"one unpaginated staff read already disagrees with the admin view")

	for _, size := range []int{1, 2, 3, 7, 500} {
		t.Run(fmt.Sprintf("limit=%d", size), func(t *testing.T) {
			var got []string
			offset := 0
			for guard := 0; guard < 200; guard++ {
				url := fmt.Sprintf("/api/v1/admin/audit?limit=%d&offset=%d", size, offset)
				resp := h.do(t, http.MethodGet, url, nil)
				var body struct {
					Entries []struct {
						ID string `json:"id"`
					} `json:"entries"`
					Total   *int `json:"total"`
					HasMore bool `json:"has_more"`
				}
				decodeJSON(t, resp, &body)
				resp.Body.Close()

				require.NotNil(t, body.Total)
				require.Equal(t, len(want), *body.Total, "the total is not the size of the visible sequence")
				require.LessOrEqual(t, len(body.Entries), size, "a page came back longer than limit")

				for _, e := range body.Entries {
					got = append(got, e.ID)
				}
				if !body.HasMore {
					break
				}
				offset += size
			}
			// Equality, not "no duplicates": this catches a skipped entry as
			// well as a repeated one, and catches reordering.
			require.Equal(t, want, got,
				"paging at limit=%d did not reproduce the visible sequence", size)
		})
	}
}

// staffAuditIDs reads the staff view in one request.
func staffAuditIDs(t *testing.T, h *harness, limit, offset int) []string {
	t.Helper()
	resp := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/audit?limit=%d&offset=%d", limit, offset), nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	decodeJSON(t, resp, &body)
	ids := make([]string, 0, len(body.Entries))
	for _, e := range body.Entries {
		ids = append(ids, e.ID)
	}
	return ids
}

// An offset past the end is an empty page, not an error and not a wrap.
func TestAdminAudit_StaffOffsetPastTheEnd(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	createAndResolveTicket(t, h)
	enableScope(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?limit=5&offset=10000", nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Entries []any `json:"entries"`
		HasMore bool  `json:"has_more"`
	}
	decodeJSON(t, resp, &body)
	require.Empty(t, body.Entries)
	require.False(t, body.HasMore, "claimed another page beyond the end")
}

// plantAuditEntryBefore is plantAuditEntry for the Before map.
func plantAuditEntryBefore(t *testing.T, h *harness, entityType string, entityID uuid.UUID, before map[string]any) {
	t.Helper()
	require.NoError(t, h.auditStore.Create(context.Background(), audit.Entry{
		ID:         uuid.New(),
		EntityType: entityType,
		EntityID:   entityID,
		Action:     "fixture_sensitive_before",
		Before:     before,
		CreatedAt:  time.Now(),
	}))
}

// The settings this PR adds are refused to a machine credential, which is the
// whole point of putting them on AuthCriticalKeys: shortening retention
// destroys evidence, and a leaked API key that performed a credential reset
// must not be able to erase the record of it.
func TestAuditSettings_RefusedToAMachineCredential(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, body := range []map[string]any{
		{"audit_retention_days": 1},
		{"staff_can_view_ticket_change_history": true},
		// Mixed with something innocuous: the whole request must be refused,
		// not quietly applied minus the critical key.
		{"site_name": "x", "audit_retention_days": 1},
	} {
		resp := h.doUnauthWithHeaders(t, http.MethodPatch, "/api/v1/admin/settings", body,
			map[string]string{"Authorization": "ApiKey " + h.adminKey})
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode,
			"an API key changed %v", body)
	}
}

// And the validation, at the layer it lives in rather than only in a comment.
func TestAuditSettings_Validation(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		// null was the gap: plain json.Unmarshal treats it as a no-op that
		// returns no error, so this answered 204 and silently turned a
		// configured window into "forever". Third time in this repo.
		{"retention null", map[string]any{"audit_retention_days": nil}, http.StatusBadRequest},
		{"toggle null", map[string]any{"staff_can_view_ticket_change_history": nil}, http.StatusBadRequest},
		{"retention as a string", map[string]any{"audit_retention_days": "90"}, http.StatusBadRequest},
		{"retention as a float", map[string]any{"audit_retention_days": 90.5}, http.StatusBadRequest},
		{"toggle as a string", map[string]any{"staff_can_view_ticket_change_history": "false"}, http.StatusBadRequest},
		// A value large enough to wrap AddDate turns the sweep's cutoff into
		// a future date, which deletes the whole log — the opposite of what
		// somebody typing a huge number means.
		{"retention large enough to wrap", map[string]any{"audit_retention_days": int64(9223372036854775807)}, http.StatusBadRequest},
		{"retention just over the cap", map[string]any{"audit_retention_days": admin.AuditRetentionMaxDays + 1}, http.StatusBadRequest},
		// Accepted: a sane window, and the two ways of saying forever.
		{"a sane window", map[string]any{"audit_retention_days": 90}, http.StatusNoContent},
		{"zero is forever", map[string]any{"audit_retention_days": 0}, http.StatusNoContent},
		{"negative is forever", map[string]any{"audit_retention_days": -5}, http.StatusNoContent},
		// Named, not spelled: the cap moved from 36500 to 36525 once somebody
		// noticed that 100 x 365 is two dozen days short of a Gregorian
		// century, and a test with the old number baked in would have gone on
		// passing while asserting the wrong boundary.
		{"the cap itself", map[string]any{"audit_retention_days": admin.AuditRetentionMaxDays}, http.StatusNoContent},
	}
	// A signed-in administrator, not the admin API key: these keys are
	// auth-critical now, so a machine credential is refused before validation
	// ever runs — which is what the test above asserts.
	sess := adminSession(t, h)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", tc.body)
			resp.Body.Close()
			require.Equal(t, tc.want, resp.StatusCode)
		})
	}
}

// adminAuditIDsFor is the admin view's ticket entries, in order, restricted to
// the given ticket ids.
func adminAuditIDsFor(t *testing.T, h *harness, tickets []string) []string {
	t.Helper()
	keep := map[string]bool{}
	for _, id := range tickets {
		keep[id] = true
	}
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket&limit=500", nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Entries []struct {
			ID       string `json:"id"`
			EntityID string `json:"entity_id"`
		} `json:"entries"`
	}
	decodeJSON(t, resp, &body)
	var ids []string
	for _, e := range body.Entries {
		if keep[e.EntityID] {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// Entries written in the same instant come back in one fixed order — id
// descending — whatever the page size, for every kind of viewer.
//
// created_at alone does not order them, and a listing is read a page at a time;
// two queries that disagree about which tied row comes first can show an entry
// twice or not at all. The tiebreaker was added in round 2 of #328's review
// with no test, and removing it passed everything because no fixture ever
// planted two rows in the same microsecond. Staff are covered separately from
// the admin because the scoped query is a different statement shape (a join
// against tickets), and an ordering that holds for one need not hold for the
// other.
func TestAdminAudit_TiedTimestampsHaveOneOrder(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	ticketID := uuid.MustParse(createAndResolveTicket(t, h)) // staff's own, so in scope
	at := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	var planted []string
	for i := 0; i < 8; i++ {
		id := uuid.New()
		planted = append(planted, id.String())
		// Not h.auditStore.Create: it stamps time.Now() and would never tie.
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: id, EntityType: "ticket", EntityID: ticketID, Action: "fixture_tie", CreatedAt: at,
		}))
	}
	// Expected: id descending. Random UUIDs make the insertion order and the
	// id order disagree with overwhelming likelihood (1 in 8! that they match),
	// so a query that falls back to heap order fails here.
	want := append([]string(nil), planted...)
	sort.Sort(sort.Reverse(sort.StringSlice(want)))

	viewers := []struct {
		name     string
		do       func(*testing.T, string, string, any) *http.Response
		enforced bool
	}{
		{"admin", h.doAsAdmin, false},
		{"staff without scope", h.do, false},
		{"staff under scope", h.do, true},
	}
	for _, v := range viewers {
		if v.enforced {
			enableScope(t, h)
		}
		for _, size := range []int{1, 3, 100} {
			var got []string
			for offset := 0; offset < 50; offset += size {
				resp := v.do(t, http.MethodGet,
					fmt.Sprintf("/api/v1/admin/audit?action=fixture_tie&limit=%d&offset=%d", size, offset), nil)
				var body struct {
					Entries []struct {
						ID string `json:"id"`
					} `json:"entries"`
				}
				decodeJSON(t, resp, &body)
				resp.Body.Close()
				for _, e := range body.Entries {
					got = append(got, e.ID)
				}
				if len(body.Entries) < size {
					break
				}
			}
			require.Equal(t, want, got, "%s: tied entries at limit=%d are not in id-descending order", v.name, size)
		}
	}
}

// With scope enforcement off — the default — staff may see every ticket, so
// their view of ticket entries must be the admin's view: same entries, same
// paging, and entries on tickets that no longer load included, since there is
// no scope to apply and nothing to be hidden by. Round 4 of #328's review
// measured staff on the default config getting nothing for deleted tickets,
// where admin got full pages.
func TestAdminAudit_StaffWithoutScopeSeeTheAdminView(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Entries on tickets that do not exist (deleted, or never loaded).
	for i := 0; i < 12; i++ {
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "fixture_open",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}))
	}

	page := func(doer func(*testing.T, string, string, any) *http.Response) ([]string, bool, int) {
		resp := doer(t, http.MethodGet, "/api/v1/admin/audit?action=fixture_open&limit=5&offset=5", nil)
		defer resp.Body.Close()
		var body struct {
			Entries []struct {
				ID string `json:"id"`
			} `json:"entries"`
			HasMore bool `json:"has_more"`
			Total   int  `json:"total"`
		}
		decodeJSON(t, resp, &body)
		var ids []string
		for _, e := range body.Entries {
			ids = append(ids, e.ID)
		}
		return ids, body.HasMore, body.Total
	}
	adminIDs, adminMore, adminTotal := page(h.doAsAdmin)
	staffIDs, staffMore, staffTotal := page(h.do)

	require.Len(t, adminIDs, 5)
	require.Equal(t, adminIDs, staffIDs, "staff without scope did not get the admin's page")
	require.Equal(t, adminMore, staffMore)
	require.Equal(t, 12, adminTotal)
	require.Equal(t, adminTotal, staffTotal)
}

// The admin pager runs on has_more. Round 4 mutated it to always-true and to
// always-false and the backend suite passed both.
func TestAdminAudit_AdminHasMore(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for i := 0; i < 2; i++ {
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "fixture_more",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}))
	}
	for _, tc := range []struct {
		offset int
		want   bool
	}{{0, true}, {1, false}} {
		resp := h.doAsAdmin(t, http.MethodGet,
			fmt.Sprintf("/api/v1/admin/audit?action=fixture_more&limit=1&offset=%d", tc.offset), nil)
		var body struct {
			HasMore bool `json:"has_more"`
		}
		decodeJSON(t, resp, &body)
		resp.Body.Close()
		require.Equal(t, tc.want, body.HasMore, "offset %d", tc.offset)
	}
}

// A NUL byte cannot be stored in a Postgres text value, so passing one
// through to the query made a 500 out of a malformed request.
func TestAdminAudit_NulInAFilterIsABadRequest(t *testing.T) {
	for _, q := range []string{"action=%00", "entity_type=%00", "q=a%00b"} {
		t.Run(q, func(t *testing.T) {
			// A harness per request: a failed statement aborts the harness
			// transaction, and every later request would then fail for that
			// reason instead of its own.
			h, cleanup := newHarness(t)
			defer cleanup()
			resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?"+q, nil)
			resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

// The no-scope staff page sets has_more by reading one row past the page.
// A page that ends exactly on the last row has no next page; round 6
// changed `>` to `>=` there and nothing failed.
func TestAdminAudit_StaffWithoutScopeExactlyFullLastPage(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for i := 0; i < 4; i++ {
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "fixture_exact",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}))
	}
	for _, tc := range []struct {
		offset int
		want   bool
	}{{0, true}, {2, false}} {
		resp := h.do(t, http.MethodGet,
			fmt.Sprintf("/api/v1/admin/audit?action=fixture_exact&limit=2&offset=%d", tc.offset), nil)
		var body struct {
			Entries []any `json:"entries"`
			HasMore bool  `json:"has_more"`
		}
		decodeJSON(t, resp, &body)
		resp.Body.Close()
		require.Len(t, body.Entries, 2)
		require.Equal(t, tc.want, body.HasMore, "offset %d", tc.offset)
	}
}

// Under scope, an entry whose ticket cannot be loaded — deleted, or the
// lookup failed — is hidden from staff. The code said "fails closed" in a
// comment; round 6 made it fail open and every test passed.
func TestAdminAudit_ScopedStaffDoNotSeeEntriesOnMissingTickets(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for i := 0; i < 3; i++ {
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "fixture_orphan",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}))
	}
	enableScope(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?action=fixture_orphan", nil)
	defer resp.Body.Close()
	var body struct {
		Entries []any `json:"entries"`
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, body.Entries, "a scoped staff member saw entries on tickets that could not be loaded")
}

// An entry on a ticket the staff member may not see is indistinguishable from
// an entry on a ticket that does not exist: the whole response is the same.
// That is the 404 policy docs/DESIGN.md sets for REST and MCP (#174), applied
// to a listing — if the two answered differently, the audit view would be an
// oracle for which ticket ids exist.
func TestAdminAudit_HiddenAndMissingTicketsAreIndistinguishable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	resp.Body.Close()
	exists := uuid.MustParse(created.ID)

	for action, entity := range map[string]uuid.UUID{"fixture_hidden": exists, "fixture_missing": uuid.New()} {
		for i := 0; i < 3; i++ {
			require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
				ID: uuid.New(), EntityType: "ticket", EntityID: entity, Action: action,
				CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
			}))
		}
	}
	enableScope(t, h)

	read := func(action string) string {
		resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?action="+action, nil)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		raw, err := readAllBody(resp)
		require.NoError(t, err)
		return raw
	}
	hidden, missing := read("fixture_hidden"), read("fixture_missing")
	require.Equal(t, missing, hidden, "a hidden ticket's entries answered differently from a missing ticket's")
	require.Contains(t, hidden, `"total":0`)
}
