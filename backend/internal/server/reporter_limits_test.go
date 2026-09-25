package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// What a reporting user may set on a ticket, against what the design says
// they may set.
//
// Each of these was reachable. None is an escalation to another account's
// data; all four are a reporter doing something the role table says is not
// theirs, on a request the shipped UI never sends — which is exactly the
// shape that goes unnoticed until somebody reads the handler.
func TestCreateTicket_AReporterIsHeldToTheRoleTable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("priority is not theirs to choose", func(t *testing.T) {
		// A reporter setting their own ticket to critical is a queue every
		// account holder can jump. The guest form has never taken priority
		// from the request; this handler forgot.
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Urgent to me", "description": "x",
			"category_id": h.catID.String(), "priority": "critical",
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)

		var created ticket.Ticket
		require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
		require.Equal(t, ticket.PriorityMedium, created.Priority,
			"the reporter set their own priority")
	})

	t.Run("an archived category is closed to them", func(t *testing.T) {
		archived, err := h.categorySvc.CreateCategory(ctx, "Retired", 99)
		require.NoError(t, err)
		archived.Active = false
		require.NoError(t, h.categorySvc.UpdateCategory(ctx, archived))

		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Into the archive", "description": "x",
			"category_id": archived.ID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"an administrator took this category out of circulation; keeping the id should not get you back in")
	})

	t.Run("an archived type is closed to them, and still open to staff", func(t *testing.T) {
		// The same rule as the category above, one level down. The picker a
		// reporter is shown lists active types only, so an archived id came
		// from somewhere else — but the handler only checked that the type
		// belonged to the category, so it went through.
		retired, err := h.categorySvc.CreateType(ctx, h.catID, "Retired type", 97)
		require.NoError(t, err)
		retired.Active = false
		require.NoError(t, h.categorySvc.UpdateType(ctx, retired))

		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Under a retired type", "description": "x",
			"category_id": h.catID.String(), "type_id": retired.ID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"an administrator took this type out of circulation; keeping the id should not get you back in")

		// Staff are not held to it: filing an old ticket under the
		// classification it actually belongs to is ordinary work.
		staffRes := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Staff filing under a retired type", "description": "x",
			"category_id": h.catID.String(), "type_id": retired.ID.String(),
		})
		defer staffRes.Body.Close()
		require.Equal(t, http.StatusCreated, staffRes.StatusCode,
			"staff were stopped from using an archived classification")
	})

	t.Run("a type from another category is refused before a number is taken", func(t *testing.T) {
		// The database has a foreign key on the pair, but it fires at the
		// INSERT — which happens after the tracking number has been taken
		// from the sequence. So the request answered 500 and left a hole in
		// the numbering: ...000001, a failure, then ...000003.
		other, err := h.categorySvc.CreateCategory(ctx, "Elsewhere", 98)
		require.NoError(t, err)
		foreign, err := h.categorySvc.CreateType(ctx, other.ID, "Foreign type", 1)
		require.NoError(t, err)

		before := latestTicketNumber(t, h)

		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Mismatched", "description": "x",
			"category_id": h.catID.String(), "type_id": foreign.ID.String(),
		})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"a mismatched pair should be refused, not answered with an internal error")

		after := createTicketAsUser(t, h, "After the failure")
		require.Equal(t, before+1, trackingSeq(t, after.TrackingNumber),
			"the failed request consumed a tracking number")
	})

	t.Run("a field the ticket does not have cannot be written", func(t *testing.T) {
		// Assigned to no scope, so it is offered to nobody and rendered
		// nowhere — the shape an administrator uses for a field they are
		// setting up, or one they have deliberately kept off the reporter's
		// form.
		def, err := h.customFieldSvc.CreateFieldDef(ctx, "Cost centre", "text", nil, 1)
		require.NoError(t, err)

		tk := createTicketAsUser(t, h, "Mine")

		res := h.doAsUser(t, http.MethodPut,
			"/api/v1/tickets/"+tk.ID.String()+"/custom-fields",
			map[string]string{def.ID.String(): "someone else's budget"})
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"a value was stored against a field this ticket does not have")
	})
}

// A link is a staff judgement about the queue. The reporter does not get to
// undo it.
//
// Removal had no role check and no check on the other ticket — unlike adding
// one, which has both. So a reporter could delete "duplicate of
// GHD-2026-000123" from their own ticket, and could tell a real target id
// from an invented one by the difference between 204 and a failure.
func TestRemoveLink_IsStaffOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// BOTH tickets belong to the reporter, deliberately.
	//
	// If one of them were a staff ticket the reporter cannot see, the
	// can-you-see-the-other-end check would refuse the request on its own and
	// this test would pass whether or not the role check exists. Two tickets
	// they own leaves the role check as the only thing that can say no.
	mine := createTicketAsUser(t, h, "Mine")
	alsoMine := createTicketAsUser(t, h, "Also mine")
	_ = ctx

	// Staff mark one a duplicate of the other — a judgement about the queue.
	add := h.do(t, http.MethodPost, "/api/v1/tickets/"+mine.ID.String()+"/links",
		map[string]any{"target_id": alsoMine.ID.String(), "link_type": "duplicate_of"})
	add.Body.Close()
	require.Equal(t, http.StatusNoContent, add.StatusCode)

	// The reporter tries to take it off.
	res := h.doAsUser(t, http.MethodDelete,
		"/api/v1/tickets/"+mine.ID.String()+"/links/"+alsoMine.ID.String()+"/duplicate_of", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"the reporter removed a link staff had put on the ticket")

	// And staff still can.
	ok := h.do(t, http.MethodDelete,
		"/api/v1/tickets/"+mine.ID.String()+"/links/"+alsoMine.ID.String()+"/duplicate_of", nil)
	ok.Body.Close()
	require.Equal(t, http.StatusNoContent, ok.StatusCode)
}

// createTicketAsUser files a ticket as the seeded reporting user, over HTTP,
// so the handler's own restrictions apply.
func createTicketAsUser(t *testing.T, h *harness, subject string) ticket.Ticket {
	t.Helper()
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": subject, "description": "x", "category_id": h.catID.String(),
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var created ticket.Ticket
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	return created
}

// latestTicketNumber is the sequence number most recently handed out, read by
// taking one. Cheap, and it needs no access to the sequence itself.
func latestTicketNumber(t *testing.T, h *harness) int {
	t.Helper()
	return trackingSeq(t, createTicketAsUser(t, h, "Probe").TrackingNumber)
}

// trackingSeq pulls the counter off the end of a tracking number, e.g.
// GHD-2026-000007 → 7.
func trackingSeq(t *testing.T, n ticket.TrackingNumber) int {
	t.Helper()
	parts := strings.Split(string(n), "-")
	require.Len(t, parts, 3, "unexpected tracking number %q", n)
	seq, err := strconv.Atoi(parts[2])
	require.NoError(t, err)
	return seq
}
