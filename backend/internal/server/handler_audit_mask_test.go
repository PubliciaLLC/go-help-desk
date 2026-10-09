package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

type maskEntry struct {
	EntityID    string  `json:"entity_id"`
	Action      string  `json:"action"`
	ActorID     *string `json:"actor_id"`
	ActorName   string  `json:"actor_name"`
	ActorMasked bool    `json:"actor_masked"`
}

// createTicketVia files a ticket through do (h.do = staff key, h.doAsUser = requester).
func createTicketVia(t *testing.T, h *harness, do func(*testing.T, string, string, any) *http.Response) string {
	t.Helper()
	res := do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "mask test", "category_id": h.catID.String(), "priority": "low"})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var c struct {
		ID string `json:"id"`
	}
	decodeJSON(t, res, &c)
	res.Body.Close()
	return c.ID
}

// maskEntriesFor returns the given ticket's entries from the admin-wide log ("admin") or its feed ("ticket").
func maskEntriesFor(t *testing.T, h *harness, view, ticketID string) []maskEntry {
	t.Helper()
	if view == "ticket" {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/audit", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var es []maskEntry
		decodeJSON(t, res, &es)
		res.Body.Close()
		return es
	}
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket&limit=500", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var page struct {
		Entries []maskEntry `json:"entries"`
	}
	decodeJSON(t, res, &page)
	res.Body.Close()
	var out []maskEntry
	for _, e := range page.Entries {
		if e.EntityID == ticketID {
			out = append(out, e)
		}
	}
	return out
}

func maskEntryFor(t *testing.T, es []maskEntry, action string) maskEntry {
	t.Helper()
	for _, e := range es {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("no %q entry in %+v", action, es)
	return maskEntry{}
}

func TestAudit_MaskedActorIsFlaggedSoARealRequesterNameIsNot(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.userSvc.UpdateProfile(ctx, h.adminID, "admin@test.local", "Requester"))

	id := createTicketVia(t, h, h.doAsUser)
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+id, map[string]any{"assignee_user_id": h.staffID.String()})
	require.Equal(t, http.StatusOK, res.StatusCode)
	res.Body.Close()

	for _, view := range []string{"admin", "ticket"} {
		t.Run(view, func(t *testing.T) {
			es := maskEntriesFor(t, h, view, id)
			created := maskEntryFor(t, es, "created")
			require.Equal(t, "Requester", created.ActorName)
			require.True(t, created.ActorMasked)
			require.NotNil(t, created.ActorID)

			assigned := maskEntryFor(t, es, "assigned")
			require.Equal(t, "Requester", assigned.ActorName, "the admin's own display name")
			require.False(t, assigned.ActorMasked)
		})
	}

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAuditMaskRequesterNames, "ticket_log"))
	created := maskEntryFor(t, maskEntriesFor(t, h, "admin", id), "created")
	require.Equal(t, "Reporting User", created.ActorName)
	require.False(t, created.ActorMasked)

	// Backwards compatibility: an unmasked entry does not carry the key at all.
	res = h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+id+"/audit", nil)
	body, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()
	var raw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &raw))
	foundAssigned := false
	for _, m := range raw {
		var action string
		require.NoError(t, json.Unmarshal(m["action"], &action))
		if action == "assigned" {
			foundAssigned = true
			require.NotContains(t, m, "actor_masked")
		}
	}
	require.True(t, foundAssigned, "no assigned entry in %s", body)
}

// The admin-wide view has its own actor_masked tag (adminAuditEntryView), so the
// per-ticket raw-JSON check above does not cover it: an unmasked entry must not
// carry the key, a masked one must carry it as true.
func TestAudit_AdminViewCarriesActorMaskedOnlyWhenTrue(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	id := createTicketVia(t, h, h.doAsUser)
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+id, map[string]any{"assignee_user_id": h.staffID.String()})
	require.Equal(t, http.StatusOK, res.StatusCode)
	res.Body.Close()

	res = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?entity_type=ticket&limit=500", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := readAllBody(res)
	require.NoError(t, err)
	res.Body.Close()
	var page struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page))

	entryFor := func(action string) map[string]json.RawMessage {
		t.Helper()
		for _, m := range page.Entries {
			var entityID, act string
			require.NoError(t, json.Unmarshal(m["entity_id"], &entityID))
			require.NoError(t, json.Unmarshal(m["action"], &act))
			if entityID == id && act == action {
				return m
			}
		}
		t.Fatalf("no %q entry for %s in %s", action, id, body)
		return nil
	}

	var masked bool
	require.NoError(t, json.Unmarshal(entryFor("created")["actor_masked"], &masked))
	require.True(t, masked)
	require.NotContains(t, entryFor("assigned"), "actor_masked")
}

func TestAudit_RequesterMaskReadsTheActorsCurrentRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	reqTicket := createTicketVia(t, h, h.doAsUser)
	staffTicket := createTicketVia(t, h, h.do)
	require.NoError(t, h.userSvc.SetRole(ctx, h.userID, user.RoleStaff))
	require.NoError(t, h.userSvc.SetRole(ctx, h.staffID, user.RoleUser))

	// Promoted since filing: named.
	promoted := maskEntryFor(t, maskEntriesFor(t, h, "admin", reqTicket), "created")
	require.Equal(t, "Reporting User", promoted.ActorName)
	require.False(t, promoted.ActorMasked)

	// Demoted since filing: masked.
	demoted := maskEntryFor(t, maskEntriesFor(t, h, "admin", staffTicket), "created")
	require.Equal(t, "Requester", demoted.ActorName)
	require.True(t, demoted.ActorMasked)
}

func TestSettings_DumpReportsTheMaskingValueInForce(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	dump := func(t *testing.T) map[string]json.RawMessage {
		t.Helper()
		res, body := sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		var got map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &got))
		return got
	}

	t.Run("unset stays absent", func(t *testing.T) {
		require.NotContains(t, dump(t), admin.KeyAuditMaskRequesterNames)
	})

	for _, tc := range []struct {
		stored, want string
	}{
		{`"nowhere"`, `"everywhere"`},
		{`42`, `"everywhere"`},
		{`"ADMIN_LOG"`, `"everywhere"`},
		{`"admin_log"`, `"admin_log"`},
		{`"ticket_log"`, `"ticket_log"`},
	} {
		t.Run("stored "+tc.stored, func(t *testing.T) {
			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAuditMaskRequesterNames, []byte(tc.stored)))
			got := dump(t)
			require.JSONEq(t, tc.want, string(got[admin.KeyAuditMaskRequesterNames]))

			// Round trip: the page sends back what it shows, and the PATCH must accept it.
			var v string
			require.NoError(t, json.Unmarshal(got[admin.KeyAuditMaskRequesterNames], &v))
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyAuditMaskRequesterNames: v})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
		})
	}

	t.Run("the write is still guarded", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAuditMaskRequesterNames, []byte(`"nowhere"`)))
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{admin.KeyAuditMaskRequesterNames: "nowhere"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
		require.Equal(t, admin.MaskRequesterNamesEverywhere, h.adminSvc.AuditMaskRequesterNames(ctx))
	})
}
