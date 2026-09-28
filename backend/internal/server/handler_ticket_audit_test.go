package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
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

// The withholding added for #129's assigned/unassigned gap is a denylist
// (role == RoleUser && action is assigned/unassigned), not an allowlist —
// two ways that shape could quietly go wrong: it could forget the
// "unassigned" half (only ever exercised via a deleted user's tickets
// returning to the queue, never over a plain PATCH), or it could withhold
// too much and start hiding actors a reporting user is meant to see (every
// non-assignment action). This test drives a real "unassigned" entry and
// checks both directions at once.
func TestListTicketAudit_WithholdsUnassignedTooButNotOtherActions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

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
	ticketID, err := uuid.Parse(created.ID)
	require.NoError(t, err)

	// A real "unassigned" entry: assign a soon-to-depart staff member, then
	// delete them — TestDeleteUser_ReturnsTheirOpenTicketsToTheQueue already
	// proves this is the one path that writes "unassigned" rather than a
	// second "assigned". UnassignForUser only touches OPEN tickets, so this
	// has to happen before the ticket is resolved below, not after.
	leaver, err := h.userSvc.Create(ctx, userInput("leaver-audit@test.local", "A Leaver"))
	require.NoError(t, err)
	_, err = h.ticketSvc.Assign(ctx, ticketID, &leaver.ID, nil, ticket.SystemActor)
	require.NoError(t, err)

	resp = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/users/"+leaver.ID.String(), nil)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Resolving is a non-assignment, staff/admin-only action with no
	// disclosure restriction — a reporting user must still see who did it,
	// same as /history already shows them. This is what "withhold too much"
	// would break.
	resp = h.do(t, http.MethodPost, "/api/v1/tickets/"+created.ID+"/resolve", map[string]any{})
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

	var sawResolved, sawUnassigned bool
	for _, e := range entries {
		switch e.Action {
		case "resolved":
			sawResolved = true
			require.NotEmpty(t, e.ActorName, "a reporting user must still see who resolved their ticket")
		case "unassigned":
			sawUnassigned = true
			require.Nil(t, e.ActorID, "a reporting user must not learn who unassigned their ticket either")
			require.Empty(t, e.ActorName, "a reporting user must not learn who unassigned their ticket either")
		}
	}
	require.True(t, sawResolved, "expected a resolved entry")
	require.True(t, sawUnassigned, "expected an unassigned entry from the deleted user's ticket being returned to the queue")
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
