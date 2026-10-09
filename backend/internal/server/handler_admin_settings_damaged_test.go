package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// TestSettings_ReputationRefusalsAreUnchanged validates that the reputation
// settings endpoint refuses invalid configurations with the correct status,
// code and message. Before each row, all reputation toggles and keys are reset
// to their empty/false defaults so that rows are independent.
func TestSettings_ReputationRefusalsAreUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	// Reset all reputation settings to defaults: toggles to false, keys to "".
	resetReputationSettings := func(t *testing.T) {
		t.Helper()
		for _, p := range reputation.ProviderNames() {
			enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
			if !ok {
				continue
			}
			require.NoError(t, h.adminSvc.SetBool(ctx, enabledKey, false))
			if apiKeyKey != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, apiKeyKey, ""))
			}
		}
	}

	cases := []struct {
		name        string
		setup       func(*testing.T) // stored state before PATCH
		body        map[string]any
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			"virustotal enabled without key",
			nil,
			map[string]any{"attachment_reputation_virustotal_enabled": true},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"VirusTotal cannot be enabled without an API key",
		},
		{
			"metadefender enabled without key",
			nil,
			map[string]any{"attachment_reputation_metadefender_enabled": true},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"MetaDefender cannot be enabled without an API key",
		},
		{
			"polyswarm enabled without key",
			nil,
			map[string]any{"attachment_reputation_polyswarm_enabled": true},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"PolySwarm cannot be enabled without an API key",
		},
		{
			"virustotal toggle not boolean",
			nil,
			map[string]any{"attachment_reputation_virustotal_enabled": "yes"},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"the VirusTotal toggle must be true or false",
		},
		{
			"circl toggle not boolean",
			nil,
			map[string]any{"attachment_reputation_circl_enabled": "yes"},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"the CIRCL toggle must be true or false",
		},
		{
			"virustotal key not string",
			nil,
			map[string]any{
				"attachment_reputation_virustotal_enabled": true,
				"attachment_reputation_virustotal_key":     42,
			},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"the VirusTotal API key must be a string",
		},
		{
			"virustotal key is whitespace only",
			nil,
			map[string]any{
				"attachment_reputation_virustotal_enabled": true,
				"attachment_reputation_virustotal_key":     "   ",
			},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"VirusTotal cannot be enabled without an API key",
		},
		{
			"virustotal stored enabled; body clears key",
			func(t *testing.T) {
				require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyAttachmentReputationVirusTotalEnabled, true))
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentReputationVirusTotalKey, "stored_key"))
			},
			map[string]any{"attachment_reputation_virustotal_key": ""},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"VirusTotal cannot be enabled without an API key",
		},
		{
			"virustotal stored enabled; body sets key to whitespace",
			func(t *testing.T) {
				require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyAttachmentReputationVirusTotalEnabled, true))
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentReputationVirusTotalKey, "stored_key"))
			},
			map[string]any{"attachment_reputation_virustotal_key": "   "},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"VirusTotal cannot be enabled without an API key",
		},
		{
			"virustotal key without toggle (toggle off)",
			nil,
			map[string]any{"attachment_reputation_virustotal_key": 42},
			http.StatusNoContent,
			"",
			"",
		},
		{
			"virustotal toggle null",
			nil,
			map[string]any{"attachment_reputation_virustotal_enabled": nil},
			http.StatusNoContent,
			"",
			"",
		},
		{
			"multiple enabled without keys",
			nil,
			map[string]any{
				"attachment_reputation_virustotal_enabled":   true,
				"attachment_reputation_metadefender_enabled": true,
			},
			http.StatusBadRequest,
			"invalid_reputation_config",
			"VirusTotal cannot be enabled without an API key",
		},
		{
			"metadefender stored enabled with key; toggle unchanged",
			func(t *testing.T) {
				require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyAttachmentReputationMetaDefenderEnabled, true))
				require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyAttachmentReputationMetaDefenderKey, "md_key"))
			},
			map[string]any{"attachment_reputation_metadefender_enabled": true},
			http.StatusNoContent,
			"",
			"",
		},
		{
			"circl enabled (no key required)",
			nil,
			map[string]any{"attachment_reputation_circl_enabled": true},
			http.StatusNoContent,
			"",
			"",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetReputationSettings(t)
			if c.setup != nil {
				c.setup(t)
			}

			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", c.body)
			require.Equal(t, c.wantStatus, res.StatusCode, "body: %s", body)

			if c.wantStatus == http.StatusNoContent {
				return
			}

			var got struct {
				Error struct {
					Code    string
					Message string
				}
			}
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, c.wantCode, got.Error.Code)
			require.Equal(t, c.wantMessage, got.Error.Message)
		})
	}
}

// TestSettings_DamagedSettingDoesNotBlockAMinimalSave validates that a
// damaged setting in the database does not block a PATCH that edits an
// unrelated setting. A minimal PATCH {"site_name": ...} must always succeed
// (204) and update the site name, even when another setting is damaged.
func TestSettings_DamagedSettingDoesNotBlockAMinimalSave(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	resetReputationSettings := func(t *testing.T) {
		t.Helper()
		for _, p := range reputation.ProviderNames() {
			enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
			if !ok {
				continue
			}
			require.NoError(t, h.adminSvc.SetBool(ctx, enabledKey, false))
			if apiKeyKey != "" {
				require.NoError(t, h.adminSvc.SetString(ctx, apiKeyKey, ""))
			}
		}
	}

	// Part (a): damage rows, each with a minimal save.
	damageRows := []struct {
		name   string
		key    string
		raw    string // raw JSON as string for readability
		repair string // clean up: raw JSON to restore valid state
	}{
		{"reputation/virustotal_enabled_true_no_key", admin.KeyAttachmentReputationVirusTotalEnabled, "true", "false"},
		{"reputation/metadefender_enabled_true_no_key", admin.KeyAttachmentReputationMetaDefenderEnabled, "true", "false"},
		{"reputation/polyswarm_enabled_true_no_key", admin.KeyAttachmentReputationPolySwarmEnabled, "true", "false"},
		{"ticket_prefix_bad_exclamation", admin.KeyTicketPrefix, `"bad!"`, `""`},
		{"ticket_prefix_null", admin.KeyTicketPrefix, "null", `""`},
		{"ticket_prefix_number", admin.KeyTicketPrefix, "42", `""`},
		{"audit_retention_days_string_x", admin.KeyAuditRetentionDays, `"x"`, "0"},
		{"audit_retention_days_null", admin.KeyAuditRetentionDays, "null", "0"},
		{"audit_retention_days_huge", admin.KeyAuditRetentionDays, "999999999", "0"},
		{"guest_submission_enabled_string_yes", admin.KeyGuestSubmissionEnabled, `"yes"`, "false"},
		{"guest_submission_enabled_null", admin.KeyGuestSubmissionEnabled, "null", "false"},
		{"staff_can_view_ticket_change_history_string_yes", admin.KeyStaffCanViewTicketChangeHistory, `"yes"`, "false"},
		{"virustotal_enabled_string_yes", admin.KeyAttachmentReputationVirusTotalEnabled, `"yes"`, "false"},
		{"attachment_scan_address_not_valid", admin.KeyAttachmentScanAddress, `"not an address"`, `""`},
		{"attachment_scan_address_number", admin.KeyAttachmentScanAddress, "42", `""`},
		{"attachment_allowed_types_exe", admin.KeyAttachmentAllowedTypes, `["exe"]`, "[]"},
		{"attachment_allowed_types_string", admin.KeyAttachmentAllowedTypes, `"x"`, "[]"},
		{"oidc_enabled_string_yes", admin.KeyOIDCEnabled, `"yes"`, "false"},
		{"oidc_enabled_true_issuer_empty", admin.KeyOIDCEnabled, "true", "false"},
	}

	for _, row := range damageRows {
		t.Run(row.name, func(t *testing.T) {
			resetReputationSettings(t)
			require.NoError(t, h.adminSvc.SetRaw(ctx, row.key, []byte(row.raw)))

			name := "Saved past " + row.name
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeySiteName: name})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
			require.Equal(t, name, h.adminSvc.SiteName(ctx))

			require.NoError(t, h.adminSvc.SetRaw(ctx, row.key, []byte(row.repair)))
		})
	}

	// Part (b): subtests on the damaged-VT state (vt_enabled=true, no key).
	t.Run("damaged_vt_state", func(t *testing.T) {
		vtDamagedTests := []struct {
			name  string
			patch map[string]any
			want  int    // 204 or 400
			check func() // optional verification after 204
		}{
			{
				"md_enabled_false",
				map[string]any{admin.KeySiteName: "test", "attachment_reputation_metadefender_enabled": false},
				http.StatusNoContent, // a different provider must not be blocked by VT's state (red on base)
				nil,
			},
			{
				"vt_key_k",
				map[string]any{admin.KeySiteName: "test", "attachment_reputation_virustotal_key": "k"},
				http.StatusNoContent,
				func() { require.Equal(t, "k", h.adminSvc.ReputationKey(ctx, reputation.ProviderVirusTotal)) },
			},
			{
				"vt_enabled_true",
				map[string]any{admin.KeySiteName: "test", "attachment_reputation_virustotal_enabled": true},
				http.StatusBadRequest,
				nil,
			},
			{
				"vt_enabled_false",
				map[string]any{admin.KeySiteName: "test", "attachment_reputation_virustotal_enabled": false},
				http.StatusNoContent,
				nil,
			},
		}

		for _, vt := range vtDamagedTests {
			t.Run(vt.name, func(t *testing.T) {
				resetReputationSettings(t)
				require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentReputationVirusTotalEnabled, []byte("true")))

				res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", vt.patch)
				require.Equal(t, vt.want, res.StatusCode, "body: %s", body)

				if vt.want == http.StatusNoContent && vt.check != nil {
					vt.check()
				}

				require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyAttachmentReputationVirusTotalEnabled, false))
			})
		}
	})
}
