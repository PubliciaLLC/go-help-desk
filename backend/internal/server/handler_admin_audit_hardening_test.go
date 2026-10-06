package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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
// tickets you have no scope over.
func TestAdminAudit_StaffAreGivenNoCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createAndResolveTicket(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit", nil) // staff session
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Total   *int `json:"total"`
		HasMore bool `json:"has_more"`
	}
	decodeJSON(t, resp, &body)
	require.Nil(t, body.Total,
		"staff were given a count, which is a count of entries they may not be able to see")
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
// never called enableScope, so nothing was out of scope: raw offset equalled
// visible offset and the pre-fix code passed it too. Verified by reverting
// scopedAuditPage to the raw-offset logic and watching it stay green. It also
// only checked for duplicates, so a fix that silently SKIPPED entries would
// have passed. Both holes are closed below — the assertion is now equality
// against the full expected sequence, not an absence of repeats.
//
// The defect it exists for: offset indexed the raw query while entries were
// the scope-filtered subset, so advancing a page skipped a different number
// of rows than the caller had been shown, and consecutive pages overlapped.
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

	// What the staff member should see, in order, taken from a single
	// unpaginated request. This is the sequence every page size must
	// reproduce exactly.
	want := staffAuditIDs(t, h, 500, 0)
	require.NotEmpty(t, want, "fixture produced nothing visible to staff")

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

				require.Nil(t, body.Total, "staff were handed a count")
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
		{"retention just over the cap", map[string]any{"audit_retention_days": 36501}, http.StatusBadRequest},
		// Accepted: a sane window, and the two ways of saying forever.
		{"a sane window", map[string]any{"audit_retention_days": 90}, http.StatusNoContent},
		{"zero is forever", map[string]any{"audit_retention_days": 0}, http.StatusNoContent},
		{"negative is forever", map[string]any{"audit_retention_days": -5}, http.StatusNoContent},
		{"the cap itself", map[string]any{"audit_retention_days": 36500}, http.StatusNoContent},
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
