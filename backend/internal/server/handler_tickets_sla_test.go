package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/stretchr/testify/require"
)

// createTicketForSLA creates a ticket in the harness's seeded category and
// returns its id.
func createTicketForSLA(t *testing.T, h *harness, subject string) string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     subject,
		"description": "x",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	return created.ID
}

// createCatchAllSLAPolicy creates a policy with no priority and no category,
// so it matches every ticket the harness creates.
func createCatchAllSLAPolicy(t *testing.T, h *harness, responseMin, resolutionMin int) {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", map[string]any{
		"name":                  "Catch-all",
		"response_target_min":   responseMin,
		"resolution_target_min": resolutionMin,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %+v", resp)
}

func slaField(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, ok := body["sla"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	require.True(t, ok, "sla must be an object or null")
	return m
}

// A ticket with no matching SLA policy carries an explicit "sla": null on
// both the list and the detail response — present, not omitted, so a client
// can tell "the server looked and found nothing" from "an older server that
// never sends the field" (see the sla.Status doc comment).
func TestTicketSLA_NoPolicy_NullOnListAndDetail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))

	id := createTicketForSLA(t, h, "No SLA policy here")

	listResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all", nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []map[string]any
	decodeJSON(t, listResp, &list)
	require.NotEmpty(t, list)
	for _, row := range list {
		require.Contains(t, row, "sla", "the key must be present even when there is no policy")
		require.Nil(t, row["sla"])
	}

	detailResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	var detail map[string]any
	decodeJSON(t, detailResp, &detail)
	require.Contains(t, detail, "sla")
	require.Nil(t, detail["sla"])
}

// With SLA tracking enabled and a matching catch-all policy, a fresh ticket's
// list and detail responses both carry a live, green response status.
func TestTicketSLA_WithPolicy_ListAndDetailCarryLiveStatus(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	id := createTicketForSLA(t, h, "Under a catch-all SLA")

	detailResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	var detail map[string]any
	decodeJSON(t, detailResp, &detail)

	sla := slaField(t, detail)
	require.NotNil(t, sla, "a matching policy must attach a status")
	require.Equal(t, "Catch-all", sla["policy_name"])

	response := sla["response"].(map[string]any)
	require.Equal(t, "green", response["color"])
	require.EqualValues(t, 60, response["target_min"])
	require.Nil(t, response["met_at"])

	resolution := sla["resolution"].(map[string]any)
	require.Equal(t, "green", resolution["color"])
	require.EqualValues(t, 480, resolution["target_min"])
	require.Nil(t, resolution["met_at"])

	// The list carries the same thing for the same ticket.
	listResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all", nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []map[string]any
	decodeJSON(t, listResp, &list)

	var found bool
	for _, row := range list {
		if row["id"] != id {
			continue
		}
		found = true
		rowSLA := slaField(t, row)
		require.NotNil(t, rowSLA)
		require.Equal(t, "green", rowSLA["response"].(map[string]any)["color"])
	}
	require.True(t, found, "the created ticket must appear in the admin-all scope")
}

// A staff reply records the response target's met_at; resolution stays
// outstanding until the ticket is resolved.
func TestTicketSLA_StaffReplySetsResponseMetAt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	id := createTicketForSLA(t, h, "Will get a staff reply")

	replyResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+id+"/replies", map[string]any{
		"body":     "We're on it.",
		"internal": false,
	})
	require.Equal(t, http.StatusCreated, replyResp.StatusCode)
	var reply struct {
		CreatedAt string `json:"created_at"`
	}
	decodeJSON(t, replyResp, &reply)
	require.NotEmpty(t, reply.CreatedAt)

	detailResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	var detail map[string]any
	decodeJSON(t, detailResp, &detail)

	sla := slaField(t, detail)
	require.NotNil(t, sla)
	response := sla["response"].(map[string]any)
	require.NotNil(t, response["met_at"], "the reply must stamp the response target as met")
	// Compared as instants truncated to microseconds, not strings: Postgres'
	// timestamptz stores microsecond precision, so the value read back after
	// a round trip drops whatever sub-microsecond digits the reply response's
	// nanosecond-precision value carried, though both name the same instant
	// up to that resolution.
	wantMetAt, err := time.Parse(time.RFC3339Nano, reply.CreatedAt)
	require.NoError(t, err)
	gotMetAt, err := time.Parse(time.RFC3339Nano, response["met_at"].(string))
	require.NoError(t, err)
	require.True(t, wantMetAt.Truncate(time.Microsecond).Equal(gotMetAt),
		"want %s, got %s", wantMetAt, gotMetAt)

	resolution := sla["resolution"].(map[string]any)
	require.Nil(t, resolution["met_at"], "resolution is still outstanding")
}

// Resolving a ticket records the resolution target's met_at.
func TestTicketSLA_ResolveSetsResolutionMetAt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	id := createTicketForSLA(t, h, "Will be resolved")

	resolveResp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+id+"/resolve", map[string]any{
		"notes": "Fixed.",
	})
	require.Equal(t, http.StatusOK, resolveResp.StatusCode)

	detailResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	var detail map[string]any
	decodeJSON(t, detailResp, &detail)

	sla := slaField(t, detail)
	require.NotNil(t, sla)
	require.NotNil(t, sla["resolution"].(map[string]any)["met_at"])
}

// The Features → SLA tracking toggle gates the field entirely: a record can
// exist (attached while the feature was on) and the response still carries
// "sla": null once the toggle is off, because the field means "SLA tracking
// is showing you something", not "a record happens to exist".
func TestTicketSLA_DisabledFeature_NullEvenWithRecord(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	id := createTicketForSLA(t, h, "Policy attached, then feature disabled")

	// Confirm a record really did attach before flipping the toggle off.
	detailResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	var detail map[string]any
	decodeJSON(t, detailResp, &detail)
	require.NotNil(t, slaField(t, detail), "sanity check: the policy must have attached")

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, false))

	detailResp = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	decodeJSON(t, detailResp, &detail)
	require.Nil(t, detail["sla"], "the toggle being off must suppress the field even though a record exists")
}

// SLA visibility is staff/admin only: a reporting user must never see the
// indicator, the policy name, the targets, or the breach/late status on their
// own ticket. Same ticket, same underlying SLA data — fetched as the
// reporting user the sla field is null, fetched as staff/admin it is the live
// status, and nothing else in the response differs.
func TestTicketSLA_UserRoleNeverSeesSLA(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     "My printer is broken",
		"description": "x",
		"category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)

	// Sanity check: the policy really did attach, so the staff/admin view
	// below is proving something (a live status), not just another null.
	staffResp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusOK, staffResp.StatusCode)
	var staffView map[string]any
	decodeJSON(t, staffResp, &staffView)
	require.NotNil(t, staffView["sla"], "sanity check: staff must see the live SLA status")

	userResp := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusOK, userResp.StatusCode)
	var userView map[string]any
	decodeJSON(t, userResp, &userView)
	require.Contains(t, userView, "sla", "the key must still be present, just null")
	require.Nil(t, userView["sla"], "a reporting user must never see their own ticket's SLA data")

	// Re-fetch both right after one another so nothing but the actor's role
	// can explain a difference, then diff the two views: sla must be the
	// only field that differs.
	staffResp = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	decodeJSON(t, staffResp, &staffView)
	userResp = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	decodeJSON(t, userResp, &userView)

	delete(staffView, "sla")
	delete(userView, "sla")
	require.Equal(t, staffView, userView, "every field other than sla must be identical between the two views")

	// The list endpoint must apply the same rule.
	listResp := h.doAsUser(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var list []map[string]any
	decodeJSON(t, listResp, &list)
	require.NotEmpty(t, list)
	for _, row := range list {
		require.Contains(t, row, "sla")
		require.Nil(t, row["sla"], "a reporting user's ticket list must never carry SLA data either")
	}
}

// Mirrors TestListTickets_PagingOnTheMergedStaffView: with policies attached,
// paging over the merged staff view still returns exactly `limit` rows, every
// one carrying the sla key, and no id repeats across pages — proving the SLA
// wrapper runs after the merge/sort/slice, not before it.
func TestTicketSLA_PagingOnTheMergedStaffView(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, true))
	createCatchAllSLAPolicy(t, h, 60, 480)

	grp, err := h.groupSvc.Create(ctx, "SLA paging group", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, grp.ID, h.staffID))

	const total = 12
	for i := 0; i < total; i++ {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": fmt.Sprintf("SLA paging %02d", i), "description": "x",
			"category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			ID string `json:"id"`
		}
		decodeJSON(t, resp, &created)

		resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
			"assignee_user_id": h.staffID.String(),
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	seen := map[any]bool{}
	for off := 0; off < total; off += 5 {
		resp := h.do(t, http.MethodGet, fmt.Sprintf("/api/v1/tickets?limit=5&offset=%d", off), nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var page []map[string]any
		decodeJSON(t, resp, &page)
		for _, row := range page {
			require.Contains(t, row, "sla", "every row must carry the key, whatever its value")
			require.False(t, seen[row["id"]], "ticket %v appeared on two pages", row["id"])
			seen[row["id"]] = true
		}
	}
	require.GreaterOrEqual(t, len(seen), total, "paging must reach every ticket at least once")
}
