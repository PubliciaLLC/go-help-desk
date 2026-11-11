package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

const auditMaskChanged = "audit_mask_changed"

func maskSettingEntries(t *testing.T, h *harness) []audit.Entry {
	t.Helper()
	pg, err := h.auditStore.Search(context.Background(), audit.Filter{EntityType: "setting", Action: auditMaskChanged}, 50, 0)
	require.NoError(t, err)
	return pg.Entries
}

// D35: a change to the requester-name mask is itself in the audit log. Loosening
// it is the one setting change that shows requester names, so it must not be
// possible to do it unrecorded.
func TestAuditMaskSetting_ChangeIsAudited(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)
	before := len(maskSettingEntries(t, h))

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAuditMaskRequesterNames: admin.MaskRequesterNamesTicketLog})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	es := maskSettingEntries(t, h)
	require.Len(t, es, before+1)
	e := es[0] // newest first
	require.NotNil(t, e.ActorID)
	require.Equal(t, h.adminID, *e.ActorID, "the administrator who changed it")
	require.Equal(t, map[string]any{"requester_name_mask": admin.MaskRequesterNamesEverywhere}, e.Before,
		"an unset setting reads as the default, which is what was in force")
	require.Equal(t, map[string]any{"requester_name_mask": admin.MaskRequesterNamesTicketLog}, e.After)

	// The log shows it, with the values: both are on the allow-list.
	r := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?action="+auditMaskChanged, nil)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var page struct {
		Entries []struct {
			Before map[string]any `json:"before"`
			After  map[string]any `json:"after"`
		} `json:"entries"`
	}
	decodeJSON(t, r, &page)
	r.Body.Close()
	require.NotEmpty(t, page.Entries)
	require.Equal(t, admin.MaskRequesterNamesTicketLog, page.Entries[0].After["requester_name_mask"])

	// Saving the same value again changes nothing, so it records nothing.
	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAuditMaskRequesterNames: admin.MaskRequesterNamesTicketLog})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Len(t, maskSettingEntries(t, h), before+1)

	// The settings page sends only what changed, but a client that sends the
	// whole dump back must not write an entry for the settings it did not touch.
	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"site_name": "Renamed"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Len(t, maskSettingEntries(t, h), before+1)

	// And narrowing it is recorded as well.
	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAuditMaskRequesterNames: admin.MaskRequesterNamesAdminLog})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	es = maskSettingEntries(t, h)
	require.Len(t, es, before+2)
	require.Equal(t, map[string]any{"requester_name_mask": admin.MaskRequesterNamesTicketLog}, es[0].Before)
	require.Equal(t, map[string]any{"requester_name_mask": admin.MaskRequesterNamesAdminLog}, es[0].After)
	require.Equal(t, admin.MaskRequesterNamesAdminLog, h.adminSvc.AuditMaskRequesterNames(ctx))
}

// A refused value changes nothing and so records nothing.
func TestAuditMaskSetting_RefusedValueIsNotAudited(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	before := len(maskSettingEntries(t, h))

	res, _ := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{admin.KeyAuditMaskRequesterNames: "nowhere"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Len(t, maskSettingEntries(t, h), before)
}
