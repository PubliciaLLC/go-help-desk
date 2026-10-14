package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// "SLA restarted" badge (#443, Erik 2026-10-13): when a priority change starts
// a new SLA record on an SLA that was already met or breached, the ticket's
// sla block carries `earlier`, the record it replaced, so the page can show
// that record's facts. One-ticket read only; never on a list row; never when
// the replaced record was still running; never to a reporting user (their sla
// is null).
func TestTicketSLA_EarlierRecord_HTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, true))
	h.ticketSvc.SetSLAEnabled(func(context.Context) bool { return true })
	for _, p := range []string{"medium", "high"} {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", map[string]any{
			"name": "Policy " + p, "priority": p, "response_target_min": 60, "resolution_target_min": 480,
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	detail := func(t *testing.T, id string) map[string]any {
		t.Helper()
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var d map[string]any
		decodeJSON(t, resp, &d)
		return d
	}
	setPriority := func(t *testing.T, id, p string) {
		t.Helper()
		resp := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+id, map[string]any{"priority": p})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	t.Run("a met response, then a priority change: the earlier record is reported", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Met, then raised")
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+id+"/replies",
			map[string]any{"body": "On it.", "internal": false})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
		setPriority(t, id, "high")

		d := detail(t, id)
		sla := slaField(t, d)
		require.NotNil(t, sla)
		require.Equal(t, "Policy high", sla["policy_name"], "the current record is under the new priority's policy")
		earlier, ok := sla["earlier"].(map[string]any)
		require.True(t, ok, "sla: %v", sla)
		require.Equal(t, "Policy medium", earlier["policy_name"])
		require.Equal(t, "met", earlier["outcome"])
		require.Equal(t, d["created_at"], earlier["started_at"], "a record that never restarted began at creation")
		require.NotEmpty(t, earlier["superseded_at"])
		require.NotEmpty(t, earlier["policy_id"])
	})

	t.Run("a running SLA re-matched: nothing restarted, no earlier", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Running, then raised")
		setPriority(t, id, "high")
		sla := slaField(t, detail(t, id))
		require.NotNil(t, sla)
		require.Equal(t, "Policy high", sla["policy_name"])
		require.NotContains(t, sla, "earlier")
	})

	t.Run("a ticket whose priority never changed has no earlier", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Untouched")
		sla := slaField(t, detail(t, id))
		require.NotNil(t, sla)
		require.NotContains(t, sla, "earlier")
	})

	t.Run("list rows never carry it", func(t *testing.T) {
		resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=all", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var list []map[string]any
		decodeJSON(t, resp, &list)
		require.NotEmpty(t, list)
		for _, row := range list {
			if sla := slaField(t, row); sla != nil {
				require.NotContains(t, sla, "earlier")
			}
		}
	})
}
