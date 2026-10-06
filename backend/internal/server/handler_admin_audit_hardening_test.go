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
	plantAuditEntry(t, h, "ticket", tid, map[string]any{
		"password_hash": secret,
		"mfa_secret":    secret,
		"subject":       "this one is not sensitive and must survive",
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

// Consecutive pages do not overlap.
//
// Measured before the fix, with in-scope and out-of-scope tickets interleaved
// and limit=2: pages came back [A B] [B C] [C D] [D E] — every page repeating
// the last entry of the one before it. The cause was that `offset` indexed the
// raw query while `entries` was the scope-filtered subset, so advancing the
// offset by a page skipped a different number of rows than the caller had
// actually been shown.
func TestAdminAudit_StaffPagesDoNotOverlap(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		createAndResolveTicket(t, h)
	}

	seen := map[string]int{}
	offset := 0
	for page := 0; page < 8; page++ {
		url := fmt.Sprintf("/api/v1/admin/audit?limit=2&offset=%d", offset)
		resp := h.do(t, http.MethodGet, url, nil) // staff session
		var body struct {
			Entries []struct {
				ID string `json:"id"`
			} `json:"entries"`
			HasMore bool `json:"has_more"`
		}
		decodeJSON(t, resp, &body)
		resp.Body.Close()

		for _, e := range body.Entries {
			seen[e.ID]++
			require.Equal(t, 1, seen[e.ID],
				"entry %s appeared on more than one page (page %d, offset %d)", e.ID, page, offset)
		}
		if !body.HasMore {
			break
		}
		offset += 2
	}
	require.NotEmpty(t, seen, "fixture produced no entries at all")
}
