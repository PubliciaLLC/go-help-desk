package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		// Since #362 a subject is free text and never shown (it can carry
		// protected data); what must survive is an allow-listed field.
		"subject":              "Student 4471 IEP, DOB 2009-03-14",
		"closed_reopen_policy": "this-one-is-allowed-and-must-survive",
	})
	plantAuditEntryBefore(t, h, "ticket", tid, map[string]any{
		"password_hash": secret,
		"token":         secret,
		"subject":       "before-side subject, also never shown",
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
			require.Contains(t, raw, "this-one-is-allowed-and-must-survive",
				"redaction removed an allow-listed field it should have shown")
			require.NotContains(t, raw, "Student 4471", "a ticket subject reached the audit view")
			require.NotContains(t, raw, "before-side subject", "a ticket subject reached the audit view")
		})
	}
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
		// Loosening it would show requester names (#362).
		{"audit_mask_requester_names": "admin_log"},
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
		// Requester-name masking (#362): one of three values, nothing else.
		{"masking null", map[string]any{"audit_mask_requester_names": nil}, http.StatusBadRequest},
		{"masking unknown", map[string]any{"audit_mask_requester_names": "nowhere"}, http.StatusBadRequest},
		{"masking as a bool", map[string]any{"audit_mask_requester_names": true}, http.StatusBadRequest},
		{"masking admin log only", map[string]any{"audit_mask_requester_names": "admin_log"}, http.StatusNoContent},
		{"masking ticket log", map[string]any{"audit_mask_requester_names": "ticket_log"}, http.StatusNoContent},
		{"masking everywhere", map[string]any{"audit_mask_requester_names": "everywhere"}, http.StatusNoContent},
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
// descending — whatever the page size.
//
// created_at alone does not order them, and a listing is read a page at a time;
// two queries that disagree about which tied row comes first can show an entry
// twice or not at all. The tiebreaker was added in round 2 of #328's review
// with no test, and removing it passed everything because no fixture ever
// planted two rows in the same microsecond.
func TestAdminAudit_TiedTimestampsHaveOneOrder(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	ticketID := uuid.MustParse(createAndResolveTicket(t, h))
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

	{
		v := struct {
			name string
			do   func(*testing.T, string, string, any) *http.Response
		}{"admin", h.doAsAdmin}
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
			resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?"+q, nil)
			resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

// #362, Erik: the admin-wide audit view is for a signed-in session, or a
// machine credential that carries audit:read. tickets:read used to reach it,
// so an admin's integration key could read every entity's audit entries —
// MFA resets, password resets — though nothing about the key said so.
func TestAdminAudit_MachineCredentialsNeedAuditRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"tickets:read is not enough", []string{"tickets:read"}, http.StatusForbidden},
		{"tickets:write is not enough", []string{"tickets:read", "tickets:write"}, http.StatusForbidden},
		{"audit:read is", []string{"audit:read"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := mintKey(t, h, tc.scopes)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit", nil)
			req.Header.Set("Authorization", "ApiKey "+key)
			rr := httptest.NewRecorder()
			h.srv.ServeHTTP(rr, req)
			require.Equal(t, tc.want, rr.Code, rr.Body.String())
		})
	}

	// A signed-in administrator needs no scope.
	s := adminSession(t, h)
	res, body := s.send(t, http.MethodGet, "/api/v1/admin/audit", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
}

// #362, Erik: staff and admin actors keep their names — the audit trail says
// which employee did what — but a requester (student, patient, customer) is
// shown as "Requester", never by name. Where, is the setting
// audit_mask_requester_names: admin_log (the admin-wide log only), ticket_log
// (each ticket's Activity feed only) or everywhere, the default. Their
// actor_id stays, so an entry is still attributable. And the free-text
// subject is left out of the diff, so an assignment no longer reads as a
// subject edit.
func TestAudit_RequesterNameMasking(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyStaffCanViewTicketChangeHistory, true))

	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Student 4471 IEP", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &created)
	res.Body.Close()
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{"assignee_user_id": h.staffID.String()})
	res.Body.Close()

	type entry struct {
		Action    string         `json:"action"`
		ActorID   *string        `json:"actor_id"`
		ActorName string         `json:"actor_name"`
		Before    map[string]any `json:"before"`
		After     map[string]any `json:"after"`
	}
	// read returns the created entry's actor name in the given view, after
	// checking the staff actor is named and the subject is absent.
	read := func(view string) string {
		var raw string
		var entries []entry
		if view == "admin" {
			res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket", nil)
			b, err := readAllBody(res)
			require.NoError(t, err)
			res.Body.Close()
			var page struct {
				Entries []entry `json:"entries"`
			}
			require.NoError(t, json.Unmarshal([]byte(b), &page))
			raw, entries = b, page.Entries
		} else {
			res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+created.ID+"/audit", nil)
			b, err := readAllBody(res)
			require.NoError(t, err)
			res.Body.Close()
			require.NoError(t, json.Unmarshal([]byte(b), &entries))
			raw = b
		}
		require.NotContains(t, raw, "Student 4471", "%s view: the subject reached the view", view)
		createdName := ""
		for _, e := range entries {
			switch e.Action {
			case "created":
				require.NotNil(t, e.ActorID, "%s view: the requester's id must stay", view)
				createdName = e.ActorName
			case "assigned":
				require.NotEqual(t, "Requester", e.ActorName, "%s view: a staff actor was masked", view)
				require.NotEmpty(t, e.ActorName)
				_, inBefore := e.Before["subject"]
				_, inAfter := e.After["subject"]
				require.False(t, inBefore || inAfter, "%s view: the subject appeared in the diff", view)
			}
		}
		require.NotEmpty(t, createdName, "%s view: no created entry", view)
		return createdName
	}

	for _, tc := range []struct {
		setting           string // "" = never set: the default
		admin, ticketFeed string
	}{
		{"", "Requester", "Requester"},
		{"everywhere", "Requester", "Requester"},
		{"admin_log", "Requester", "Reporting User"},
		{"ticket_log", "Reporting User", "Requester"},
	} {
		t.Run("setting="+tc.setting, func(t *testing.T) {
			if tc.setting != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, tc.setting))
			}
			require.Equal(t, tc.admin, read("admin"), "admin-wide log")
			require.Equal(t, tc.ticketFeed, read("ticket"), "ticket Activity feed")
		})
	}
}
