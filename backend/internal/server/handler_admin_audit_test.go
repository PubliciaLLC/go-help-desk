package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

type adminAuditEntry struct {
	ID         string         `json:"id"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Action     string         `json:"action"`
	ActorID    *string        `json:"actor_id"`
	ActorName  string         `json:"actor_name"`
	Before     map[string]any `json:"before"`
	After      map[string]any `json:"after"`
}

type adminAuditResponse struct {
	Entries []adminAuditEntry `json:"entries"`
	Total   int               `json:"total"`
}

func createAndResolveTicket(t *testing.T, h *harness) string {
	t.Helper()
	resp := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "admin audit fixture", "description": "x", "category_id": h.catID.String(),
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	require.NotEmpty(t, created.ID)

	resp = h.do(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/resolve", map[string]any{})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return created.ID
}

// A reporting user has no route here at all — RequireRole rejects before the
// handler is ever reached.
func TestAdminAudit_ReportingUserForbidden(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodGet, "/api/v1/admin/audit", nil)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A staff-level API key reaches this exactly as it reaches every other
// ticket-adjacent read (RequireResource(auth.ResourceTickets), same as
// /staff and /tags) — this is not a session-only route.
func TestAdminAudit_StaffAPIKeyAllowed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doUnauthWithHeaders(t, http.MethodGet, "/api/v1/admin/audit", nil,
		map[string]string{"Authorization": "ApiKey " + h.apiKey})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// Staff never see a non-ticket entity through this endpoint, independent of
// any filter they ask for — mfa_reset (entity_type "user") must not appear
// even though it genuinely happened.
func TestAdminAudit_StaffOnlySeesTicketEntities(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	createAndResolveTicket(t, h)
	require.NoError(t, h.userSvc.ResetMFA(ctx, h.staffID, nil))

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, e := range got.Entries {
		require.Equal(t, "ticket", e.EntityType, "staff must never see a non-ticket entry from this endpoint")
	}

	// Admin sees both kinds — the restriction is staff-specific.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit", nil)
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var sawTicket, sawUser bool
	for _, e := range got.Entries {
		sawTicket = sawTicket || e.EntityType == "ticket"
		sawUser = sawUser || e.EntityType == "user"
	}
	require.True(t, sawTicket, "expected at least one ticket entry")
	require.True(t, sawUser, "admin must see the user-entity mfa_reset entry too")
}

// Staff with ticket scope enforced must not see audit entries for a ticket
// outside their scope, even filtered to entity_type=ticket — the same
// exposure TestTicketScope_HidesOutOfScopeTicket closes for GET
// /tickets/{id} applies here too, since this is a second read path onto the
// same tickets.
func TestAdminAudit_StaffOnlySeesTicketsInScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	enableScope(t, h)

	resp = h.do(t, http.MethodGet, "/api/v1/admin/audit", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, e := range got.Entries {
		require.NotEqual(t, created.ID, e.EntityID, "staff must not see an entry for a ticket outside their scope")
	}

	// Admin is never scoped.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit", nil)
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var sawIt bool
	for _, e := range got.Entries {
		sawIt = sawIt || e.EntityID == created.ID
	}
	require.True(t, sawIt, "admin must still see the out-of-scope ticket's entry")
}

// The diff gates mirror the per-ticket feed exactly: off by default, staff
// need the setting, admin never needs it.
func TestAdminAudit_DiffHiddenFromStaffWhenSettingOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createAndResolveTicket(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?action=resolved", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, got.Entries)
	for _, e := range got.Entries {
		require.Nil(t, e.Before, "staff must not see the diff while the setting is off")
		require.Nil(t, e.After, "staff must not see the diff while the setting is off")
	}
}

func TestAdminAudit_DiffShownToStaffWhenSettingOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyStaffCanViewTicketChangeHistory, true))

	ticketID := createAndResolveTicket(t, h)

	resp := h.do(t, http.MethodGet, "/api/v1/admin/audit?action=resolved&limit=200", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	e := findEntryByEntity(t, got.Entries, ticketID)
	require.NotNil(t, e.Before)
	require.NotNil(t, e.After)
}

func TestAdminAudit_DiffAlwaysShownToAdmin(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	ticketID := createAndResolveTicket(t, h)

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action=resolved&limit=200", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	e := findEntryByEntity(t, got.Entries, ticketID)
	require.NotNil(t, e.Before)
	require.NotNil(t, e.After)
}

func findEntryByEntity(t *testing.T, entries []adminAuditEntry, entityID string) adminAuditEntry {
	t.Helper()
	for _, e := range entries {
		if e.EntityID == entityID {
			return e
		}
	}
	t.Fatalf("no entry found for entity %s", entityID)
	return adminAuditEntry{}
}

// Query-string filters actually reach the store: action=resolved must not
// return a created/closed entry alongside it.
func TestAdminAudit_ActionFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	createAndResolveTicket(t, h)

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action=resolved", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, got.Entries)
	for _, e := range got.Entries {
		require.Equal(t, "resolved", e.Action)
	}
}

func TestAdminAudit_RejectsMalformedFilters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []string{
		"/api/v1/admin/audit?actor_id=not-a-uuid",
		"/api/v1/admin/audit?from=not-a-date",
		"/api/v1/admin/audit?to=not-a-date",
	}
	for _, path := range cases {
		resp := h.doAsAdmin(t, http.MethodGet, path, nil)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, "path: %s", path)
	}
}

// The list itself, sanity-checked end to end: both tickets this test
// created show up among the resolved entries. Not asserting an exact total
// — this database is shared with other integration suites in this repo
// (e.g. the reset-factors CLI tests) that are not necessarily wrapped in a
// rolled-back transaction, so "total == exactly what this test wrote" is not
// a safe assumption; "what this test wrote is present" is.
func TestAdminAudit_TotalAndOrdering(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	first := createAndResolveTicket(t, h)
	second := createAndResolveTicket(t, h)

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action=resolved&limit=200", nil)
	var got adminAuditResponse
	decodeJSON(t, resp, &got)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.GreaterOrEqual(t, got.Total, 2)

	var sawFirst, sawSecond bool
	for _, e := range got.Entries {
		sawFirst = sawFirst || e.EntityID == first
		sawSecond = sawSecond || e.EntityID == second
	}
	require.True(t, sawFirst, "expected the first ticket's resolved entry")
	require.True(t, sawSecond, "expected the second ticket's resolved entry")
}
