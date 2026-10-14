package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// "SLA restarted" badge (#443, Erik 2026-10-14): the ticket's sla block carries
// `earlier`, the record a RESTART replaced, so the page can show that record's
// facts, and only after a restart. A staff or admin priority change re-matches
// the SLA and keeps its clock, so it never shows the badge, however many times
// it happens and whatever the old record had already recorded. A restart is a
// reopen's new record (a started_at of its own); nothing on this build calls
// the reopen path (#464), so the tests write the record the way it does, with
// the ticket store. One-ticket read only; never on a list row; never to a
// reporting user (their sla is null).
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
	setPriority := func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, id, p string) {
		t.Helper()
		resp := do(t, http.MethodPatch, "/api/v1/tickets/"+id, map[string]any{"priority": p})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}
	reply := func(t *testing.T, id string) {
		t.Helper()
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+id+"/replies",
			map[string]any{"body": "On it.", "internal": false})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	}
	// restart writes what a reopen writes: the current record superseded, a new
	// one with a started_at of its own and no facts (planSLARestart).
	restart := func(t *testing.T, id string) {
		t.Helper()
		tid := uuid.MustParse(id)
		cur, err := h.ticketStore.CurrentSLARecordForUpdate(ctx, tid)
		require.NoError(t, err)
		require.NotNil(t, cur)
		now := time.Now()
		require.NoError(t, h.ticketStore.SupersedeSLARecord(ctx, cur.ID, now))
		require.NoError(t, h.ticketStore.StartSLARecord(ctx, ticket.SLARecordStart{
			ID: uuid.New(), TicketID: tid, PolicyID: cur.PolicyID, StartedAt: &now,
		}))
	}

	t.Run("a met response, then staff priority changes: never an earlier record", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Met, then re-matched")
		reply(t, id)
		for _, step := range []struct {
			do   func(*testing.T, string, string, any) *http.Response
			prio string
			pol  string
		}{
			{h.doAsAdmin, "high", "Policy high"},
			{h.do, "medium", "Policy medium"},
			{h.doAsAdmin, "high", "Policy high"},
		} {
			setPriority(t, step.do, id, step.prio)
			sla := slaField(t, detail(t, id))
			require.NotNil(t, sla)
			require.Equal(t, step.pol, sla["policy_name"], "the current record follows the priority")
			require.NotContains(t, sla, "earlier", "a re-match keeps the clock; nothing restarted")
		}
	})

	t.Run("a running SLA re-matched: no earlier", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Running, then raised")
		setPriority(t, h.doAsAdmin, id, "high")
		sla := slaField(t, detail(t, id))
		require.NotNil(t, sla)
		require.Equal(t, "Policy high", sla["policy_name"])
		require.NotContains(t, sla, "earlier")
	})

	t.Run("a restarted record: the earlier record is reported", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Met, then reopened")
		reply(t, id)
		restart(t, id)

		d := detail(t, id)
		sla := slaField(t, d)
		require.NotNil(t, sla)
		earlier, ok := sla["earlier"].(map[string]any)
		require.True(t, ok, "sla: %v", sla)
		require.Equal(t, "Policy medium", earlier["policy_name"])
		require.Equal(t, "met", earlier["outcome"])
		require.Equal(t, d["created_at"], earlier["started_at"], "a record that never restarted began at creation")
		require.NotEmpty(t, earlier["superseded_at"])
		require.NotEmpty(t, earlier["policy_id"])
	})

	t.Run("a restarted record stays reported through a later staff re-match", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Reopened, then raised")
		reply(t, id)
		restart(t, id)
		setPriority(t, h.doAsAdmin, id, "high")

		sla := slaField(t, detail(t, id))
		require.NotNil(t, sla)
		require.Equal(t, "Policy high", sla["policy_name"])
		earlier, ok := sla["earlier"].(map[string]any)
		require.True(t, ok, "the clock did restart; the re-match after it does not undo that: %v", sla)
		require.Equal(t, "met", earlier["outcome"])
	})

	t.Run("a restart over a record with nothing met or breached has no outcome: no earlier", func(t *testing.T) {
		id := createTicketForSLA(t, h, "Running, then reopened")
		restart(t, id)
		sla := slaField(t, detail(t, id))
		require.NotNil(t, sla)
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

	// The reporting user has no SLA at all, so no earlier record either. The
	// control above is the same ticket read by an administrator.
	t.Run("a requester never receives it, on a ticket that restarted", func(t *testing.T) {
		resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Mine", "description": "x", "category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			ID string `json:"id"`
		}
		decodeJSON(t, resp, &created)
		reply(t, created.ID)
		restart(t, created.ID)

		staffView := slaField(t, detail(t, created.ID))
		require.NotNil(t, staffView)
		require.Contains(t, staffView, "earlier", "control: staff do see the restart")

		resp = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var own map[string]any
		decodeJSON(t, resp, &own)
		require.Nil(t, slaField(t, own), "a reporting user's sla is null")
		require.NotContains(t, own, "earlier")
	})
}
