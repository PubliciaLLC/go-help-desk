package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// #129: the per-ticket activity feed. Access control is already proven by
// TestTicketSubtree_RefusesAnUnrelatedReportingUser and
// TestTicketScope_RefusesAllSubroutesOutOfScope, which now include /audit —
// these cover what the feed actually shows a caller who IS allowed to see it.

func TestListTicketAudit_ShowsWhatHappenedOldestFirstWithTheActorNamed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Printer offline", "description": "x", "category_id": h.catID.String(),
	})
	body, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))

	// Closing is admin-only over HTTP (handleCloseTicket), so this entry's
	// actor is the admin, not the staff member who filed it — a second
	// identity to check the resolver against, not just the same one twice.
	res = h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/close", nil)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID+"/audit", nil)
	body, _ = readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	var entries []struct {
		Action    string `json:"action"`
		ActorID   string `json:"actor_id"`
		ActorName string `json:"actor_name"`
		CreatedAt string `json:"created_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &entries))
	require.NotEmpty(t, entries)

	// Not asserting an exact count: creating a ticket can also trigger
	// auto-assignment (a system-actor entry with no ActorID), which this
	// harness's default settings do not configure, but the test should not
	// break if that changes. What must hold regardless: filing is the first
	// thing recorded, and closing — the one action this test actually took —
	// is the last, both correctly attributed to the staff caller.
	require.Equal(t, "created", entries[0].Action, "oldest first")
	require.Equal(t, "Staff", entries[0].ActorName)
	last := entries[len(entries)-1]
	require.Equal(t, "closed", last.Action)
	require.Equal(t, "Admin", last.ActorName)
	for _, e := range entries {
		require.NotEmpty(t, e.CreatedAt)
	}
}

// #129 exists so a reporting user can see "who changed this and when"
// without new access-control thinking — but assignment is staff/admin-only
// (ticket.CanAssign), and nothing else discloses a staff member's identity
// to a reporting user: the ticket's own assignee_user_id is a bare UUID, and
// GET /api/v1/staff, the only place that resolves one to a name, 403s a
// RoleUser caller. A reporting user must not be able to learn who assigned
// their ticket from this feed when they could learn it nowhere else.
func TestListTicketAudit_WithholdsAssignmentActorFromReportingUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "my printer", "description": "jammed", "category_id": h.catID.String(),
	})
	body, _ := readAllBody(resp)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))

	resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
		"assignee_user_id": h.staffID.String(),
	})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID+"/audit", nil)
	body, _ = readAllBody(resp)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var entries []struct {
		Action    string  `json:"action"`
		ActorID   *string `json:"actor_id"`
		ActorName string  `json:"actor_name"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &entries))

	var sawAssigned bool
	for _, e := range entries {
		if e.Action == "assigned" {
			sawAssigned = true
			require.Nil(t, e.ActorID, "a reporting user must not learn who assigned their ticket")
			require.Empty(t, e.ActorName, "a reporting user must not learn who assigned their ticket")
		}
	}
	require.True(t, sawAssigned, "expected an assigned entry from the admin's PATCH")

	// Staff/admin get the full picture: the same feed, unwithheld.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+created.ID+"/audit", nil)
	body, _ = readAllBody(resp)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.Unmarshal([]byte(body), &entries))
	for _, e := range entries {
		if e.Action == "assigned" {
			require.NotNil(t, e.ActorID)
			require.Equal(t, "Admin", e.ActorName)
		}
	}
}

// A ticket nobody has done anything to beyond filing it still answers with
// its one entry, not an empty feed and not an error — filing IS an event.
func TestListTicketAudit_AFreshTicketHasItsCreationEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Nothing has happened yet", "description": "x", "category_id": h.catID.String(),
	})
	body, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", body)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))

	res = h.do(t, http.MethodGet, "/api/v1/tickets/"+created.ID+"/audit", nil)
	body, _ = readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var entries []struct {
		Action string `json:"action"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &entries))
	require.NotEmpty(t, entries)
	require.Equal(t, "created", entries[0].Action)
}
