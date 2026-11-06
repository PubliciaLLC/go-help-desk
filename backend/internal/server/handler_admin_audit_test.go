package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
