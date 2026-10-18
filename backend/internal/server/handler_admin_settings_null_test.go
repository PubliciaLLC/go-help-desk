package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// JSON null is refused, not stored, for the settings that took it as "set to
// nothing" (#420). The settings that already went through unmarshalSetting
// refuse it (guest_submission_enabled, the SSO fields); these did not, and a
// stored null for a toggle or an address is a value nothing can read back.
// The wrong-type refusals of these settings keep their wording.
func TestSettings_NullIsRefusedForToggleAddressAndTypes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	cases := []struct {
		key, code string
	}{
		{"attachment_reputation_virustotal_enabled", "invalid_reputation_config"},
		{"attachment_reputation_metadefender_enabled", "invalid_reputation_config"},
		{"attachment_reputation_polyswarm_enabled", "invalid_reputation_config"},
		{"attachment_reputation_circl_enabled", "invalid_reputation_config"},
		{"attachment_scan_address", "bad_request"},
		{"attachment_allowed_types", "bad_request"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{c.key: nil})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			var got struct {
				Error struct{ Code, Message string }
			}
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, c.code, got.Error.Code)
			require.Equal(t, "invalid input: "+c.key+" must not be null", got.Error.Message)

			// Nothing was stored.
			all, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)
			require.NotContains(t, all, c.key)
		})
	}
}
