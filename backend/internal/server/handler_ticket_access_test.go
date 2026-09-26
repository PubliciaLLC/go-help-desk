package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Visibility used to be enforced per handler, on GET /{id} and PATCH /{id}
// only. The fifteen routes beneath them had no check at all, so
// GET /{id}/replies returned the whole thread — staff-only internal notes
// included — to any signed-in user holding a ticket UUID, while GET /{id} on
// the same ticket correctly answered 403. Confirmed by execution before the
// fix: 403 on the ticket, 200 and the note body on its replies.
//
// The gate is now middleware on the /{id} subtree. These tests walk every route
// under it, so a route added later without thinking about access fails here
// rather than shipping.

func foreignTicket(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject:        "Exec compensation review",
		Description:    "Sensitive",
		CategoryID:     h.catID,
		Priority:       ticket.PriorityHigh,
		ReporterUserID: &h.adminID, // NOT the reporting user
	})
	require.NoError(t, err)
	return tk
}

func TestTicketSubtree_RefusesAnUnrelatedReportingUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk := foreignTicket(t, h)
	id := tk.ID.String()
	base := "/api/v1/tickets/" + id

	// Baseline: the ticket itself is refused, so any route that answers 2xx
	// below is reachable on a ticket the caller cannot read.
	res := h.doAsUser(t, http.MethodGet, base, nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "precondition")

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base, nil},
		{http.MethodPatch, base, map[string]any{"clear_assignee": true}},
		{http.MethodGet, base + "/replies", nil},
		{http.MethodPost, base + "/replies", map[string]any{"body": "injected"}},
		{http.MethodPost, base + "/resolve", map[string]any{"notes": "x"}},
		{http.MethodPost, base + "/reopen", nil},
		{http.MethodPost, base + "/close", nil},
		{http.MethodGet, base + "/links", nil},
		// Real field names: target_id and notes. With the wrong ones the
		// handler answers 400 or 500 and the 403 assertion passes for the
		// wrong reason.
		{http.MethodPost, base + "/links", map[string]any{"target_id": uuid.New().String(), "link_type": "related"}},
		{http.MethodDelete, base + "/links/" + uuid.New().String() + "/related", nil},
		{http.MethodGet, base + "/history", nil},
		{http.MethodGet, base + "/tags", nil},
		{http.MethodPost, base + "/tags", map[string]any{"name": "vip"}},
		{http.MethodDelete, base + "/tags/" + uuid.New().String(), nil},
		{http.MethodGet, base + "/attachments", nil},
		{http.MethodGet, base + "/attachments/" + uuid.New().String(), nil},
		{http.MethodGet, base + "/canned-responses", nil},
		{http.MethodGet, base + "/custom-fields", nil},
		{http.MethodPut, base + "/custom-fields", map[string]any{"values": map[string]any{}}},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s", tc.method, strings.TrimPrefix(tc.path, base)), func(t *testing.T) {
			res := h.doAsUser(t, tc.method, tc.path, tc.body)
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"a ticket the caller cannot read must not be reachable here; body %s", b)
		})
	}
}

// Internal notes are staff-to-staff. Access to a ticket is not access to them:
// the reporter may read their own thread and must still not see them.
func TestListReplies_HidesInternalNotesFromTheReporter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "My laptop will not boot", CategoryID: h.catID,
		Priority: ticket.PriorityMedium, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	newStatus := statusIDNamed(t, h, ticket.StatusNameNew)
	staffActor := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}

	_, err = h.ticketSvc.AddReply(ctx, own.ID, "We are looking into it", false, true, "", staffActor, 7, newStatus)
	require.NoError(t, err)
	_, err = h.ticketSvc.AddReply(ctx, own.ID, "INTERNAL: replace under warranty, cost code 4471", true, false, "", staffActor, 7, newStatus)
	require.NoError(t, err)

	t.Run("the reporter sees only the public reply", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+own.ID.String()+"/replies", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode, "the reporter may read their own thread")
		require.Contains(t, string(b), "We are looking into it")
		require.NotContains(t, string(b), "cost code 4471",
			"an internal note must never reach the reporter")
	})

	t.Run("staff see the whole thread", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+own.ID.String()+"/replies", nil)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Contains(t, string(b), "cost code 4471")
	})
}

// The gate resolves tracking numbers as well as UUIDs; that path must be
// access-checked too, or it becomes the way around the gate.
func TestTicketSubtree_TrackingNumberIsAlsoGated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk := foreignTicket(t, h)

	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+string(tk.TrackingNumber), nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	res = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+string(tk.TrackingNumber)+"/replies", nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"the tracking-number form must not bypass the gate")
}

func statusIDNamed(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	sts, err := h.ticketSvc.ListStatuses(context.Background())
	require.NoError(t, err)
	for _, s := range sts {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("status %q not found", name)
	return uuid.Nil
}

// The path gate authorises {id} and nothing else. handleAddLink names a SECOND
// ticket in its body, so it was the one route that genuinely addressed two
// tickets while only one was checked. Confirmed by execution before the fix:
// 403 reading the foreign ticket, 204 writing a link onto it.
func TestAddLink_ChecksTheTargetTicketToo(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	foreign := foreignTicket(t, h)

	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+own.ID.String()+"/links",
		map[string]any{"target_id": foreign.ID.String(), "link_type": "duplicate_of"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()

	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"linking TO a ticket the caller cannot read is a write onto that ticket; body %s", b)

	links, err := h.ticketSvc.ListLinks(ctx, foreign.ID)
	require.NoError(t, err)
	require.Empty(t, links, "nothing may have been written onto the foreign ticket")
}

// TestRemoveLink_ChecksTheTargetTicketToo pins #211: handleRemoveLink was
// gated only on the path's own {id}, unlike handleAddLink which checks both
// ends of the link (the link is written onto — and here, removed from — the
// TARGET's thread too). A reporting user could otherwise remove a
// staff-created link from their own ticket to a ticket they cannot see, even
// though the ids are already visible via GET /links.
func TestRemoveLink_ChecksTheTargetTicketToo(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	foreign := foreignTicket(t, h)

	// Seed the link directly through the domain service (as staff would),
	// bypassing the HTTP visibility gate that would otherwise refuse its own
	// creation against a ticket the user cannot see.
	staffActor := ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff}
	require.NoError(t, h.ticketSvc.AddLink(ctx, own.ID, foreign.ID, ticket.LinkRelatedTo, staffActor))

	res := h.doAsUser(t, http.MethodDelete,
		"/api/v1/tickets/"+own.ID.String()+"/links/"+foreign.ID.String()+"/related_to", nil)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()

	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"removing a link to a ticket the caller cannot read is a write onto that ticket; body %s", b)

	links, err := h.ticketSvc.ListLinks(ctx, own.ID)
	require.NoError(t, err)
	require.Len(t, links, 1, "the link must survive a forbidden removal attempt")
}

// TestAddLink_ReturnsInvalidLinkTypeWith400 verifies that an invalid link type
// returns 400 Bad Request, not 500 Internal Server Error.
func TestAddLink_ReturnsInvalidLinkTypeWith400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	target, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Target", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+own.ID.String()+"/links",
		map[string]any{"target_id": target.ID.String(), "link_type": "invalid_type"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()

	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"invalid link type must return 400, not 500; body: %s", b)

	var errResp map[string]any
	json.Unmarshal(b, &errResp)
	errObj := errResp["error"].(map[string]any)
	require.Equal(t, "bad_request", errObj["code"])

	// Verify no link was created
	links, err := h.ticketSvc.ListLinks(ctx, own.ID)
	require.NoError(t, err)
	require.Empty(t, links, "no link should exist after invalid type error")
}

// TestAddLink_SelfLinkReturns400 verifies that linking a ticket to itself
// returns 400 Bad Request, not 500, for both the plain AddLink path and the
// resolve-as-duplicate path. See #192.
func TestAddLink_SelfLinkReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	base := "/api/v1/tickets/" + own.ID.String() + "/links"

	t.Run("plain AddLink", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, base,
			map[string]any{"target_id": own.ID.String(), "link_type": "related_to"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "self-link must return 400, not 500; body: %s", b)

		var errResp map[string]any
		json.Unmarshal(b, &errResp)
		errObj := errResp["error"].(map[string]any)
		require.Equal(t, "cannot_link_self", errObj["code"])
	})

	t.Run("resolve-as-duplicate", func(t *testing.T) {
		res := h.do(t, http.MethodPost, base, map[string]any{
			"target_id": own.ID.String(), "link_type": "duplicate_of",
			"resolve_as_duplicate": true, "resolution_notes": "x",
		})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "self-link must return 400, not 500; body: %s", b)

		var errResp map[string]any
		json.Unmarshal(b, &errResp)
		errObj := errResp["error"].(map[string]any)
		require.Equal(t, "cannot_link_self", errObj["code"])
	})
}

// TestRemoveLink_InvalidLinkTypeReturns400 verifies that DELETE
// .../links/{targetId}/{linkType} with an unrecognized linkType returns 400
// rather than silently deleting nothing and answering 204. See #201.
func TestRemoveLink_InvalidLinkTypeReturns400(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	target, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Target", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	res := h.doAsUser(t, http.MethodDelete,
		"/api/v1/tickets/"+own.ID.String()+"/links/"+target.ID.String()+"/not_a_real_type", nil)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode,
		"an unrecognized link type must return 400, not silently answer 204; body: %s", b)
}

// TestAddLink_ReturnsDuplicateLinkWith409 verifies that creating a duplicate link
// returns 409 Conflict, not 500 Internal Server Error.
func TestAddLink_ReturnsDuplicateLinkWith409(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	target, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Target", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	// Create the link the first time
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+own.ID.String()+"/links",
		map[string]any{"target_id": target.ID.String(), "link_type": "related_to"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode, "first link creation should succeed")

	// Verify the first link was created successfully
	links, err := h.ticketSvc.ListLinks(ctx, own.ID)
	require.NoError(t, err)
	require.Len(t, links, 1, "first link should be created")

	// Try to create the same link again
	res = h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+own.ID.String()+"/links",
		map[string]any{"target_id": target.ID.String(), "link_type": "related_to"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()

	require.Equal(t, http.StatusConflict, res.StatusCode,
		"duplicate link must return 409, not 500; body: %s", b)

	var errResp map[string]any
	json.Unmarshal(b, &errResp)
	errObj := errResp["error"].(map[string]any)
	require.Equal(t, "link_already_exists", errObj["code"])
}

// Staff are the other half of the bug: with scope enforcement on, a staff
// member outside a ticket's scope must be refused on the subroutes too, not
// just on GET /{id}.
func TestTicketSubtree_RefusesStaffOutsideTheirScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyTicketScopeEnforced, true))

	// Reported by the admin, unassigned, in a category the staff user's groups
	// do not cover — so it is outside their scope entirely.
	outOfScope, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Finance escalation", CategoryID: h.catID,
		Priority: ticket.PriorityHigh, ReporterUserID: &h.adminID,
	})
	require.NoError(t, err)

	base := "/api/v1/tickets/" + outOfScope.ID.String()
	res := h.do(t, http.MethodGet, base, nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "precondition: out of scope")

	for _, path := range []string{"/replies", "/history", "/tags", "/links", "/custom-fields"} {
		t.Run(path, func(t *testing.T) {
			res := h.do(t, http.MethodGet, base+path, nil)
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"staff outside scope must be refused here too")
		})
	}
}

// Tags are how staff mark a ticket for other staff — "fraud-suspect",
// "legal-hold", "difficult-customer". DESIGN.md gives them to Staff and says
// nothing about them in the User row.
//
// Ungated, the reporting user saw the classification written about them on
// their own ticket, could delete it, and could add tags of their own to the
// global catalogue.
func TestTags_AreStaffOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "my ticket", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	base := "/api/v1/tickets/" + created.ID + "/tags"

	// Staff classify the ticket.
	resp = h.do(t, http.MethodPost, base, map[string]any{"name": "fraud-suspect"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	t.Run("the reporter cannot read the classification", func(t *testing.T) {
		resp := h.doAsUser(t, http.MethodGet, base, nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("the reporter cannot add a tag", func(t *testing.T) {
		resp := h.doAsUser(t, http.MethodPost, base, map[string]any{"name": "user-invented"})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("the reporter cannot read the global catalogue", func(t *testing.T) {
		resp := h.doAsUser(t, http.MethodGet, "/api/v1/tags", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("staff still can", func(t *testing.T) {
		resp := h.do(t, http.MethodGet, base, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp = h.do(t, http.MethodGet, "/api/v1/tags", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
