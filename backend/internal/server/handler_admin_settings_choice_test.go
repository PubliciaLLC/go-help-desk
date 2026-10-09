package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// choiceKeys is every choice-list setting, each with its accepted values and
// the reader the server enforces it through. reader is nil for the scan
// policy, whose unrecognised value is reported as unset (see choiceSettings).
var choiceKeys = []struct {
	key    string
	valid  []string
	reader func(*harness, context.Context) string
}{
	{admin.KeyClosedReopenPolicy, []string{"off", "admin", "staff_admin"},
		func(h *harness, ctx context.Context) string { return h.adminSvc.ClosedReopenPolicy(ctx) }},
	{admin.KeyAuditMaskRequesterNames, []string{"admin_log", "ticket_log", "everywhere"},
		func(h *harness, ctx context.Context) string { return h.adminSvc.AuditMaskRequesterNames(ctx) }},
	{admin.KeyAttachmentScanPolicy, []string{"off", "required", "permissive"}, nil},
	{admin.KeyAttachmentInfectedHandling, []string{"refuse", "quarantine"},
		func(h *harness, ctx context.Context) string { return h.adminSvc.InfectedHandling(ctx) }},
	{admin.KeyAttachmentMismatchHandling, []string{"refuse", "wrap"},
		func(h *harness, ctx context.Context) string { return h.adminSvc.MismatchHandling(ctx) }},
	{admin.KeyAttachmentReputationRefresh, []string{"weekly", "biweekly", "monthly", "quarterly", "never"},
		func(h *harness, ctx context.Context) string { return h.adminSvc.ReputationRefresh(ctx) }},
}

// Stored values the PATCH refuses. Only a direct database edit puts one there.
var damagedChoiceValues = []string{`"bogus"`, `""`, `42`, `null`, `true`, `["off"]`}

func settingsDump(t *testing.T, sess *session) map[string]json.RawMessage {
	t.Helper()
	res, body := sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &got))
	return got
}

// Characterisation of the refusals the PATCH gives for every choice-list
// setting (#383): status, code and message, recorded from the code before the
// shared table replaced six hand-written blocks. Two keys decode through
// unmarshalSetting (null and wrong type say "invalid input: ..."); the other
// four decode bare, so null reads as "" and gets the invalid_* refusal while a
// wrong type gets bad_request. Clients depend on each of these, so the table
// must not unify them.
func TestSettings_ChoiceSettingsRefusalsAreUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	cases := []struct {
		key, name string
		value     any
		code, msg string
	}{
		{"closed_reopen_policy", "null", nil, "bad_request", "invalid input: closed_reopen_policy must not be null"},
		{"closed_reopen_policy", "number", 42, "bad_request", "invalid input: closed_reopen_policy has the wrong type"},
		{"closed_reopen_policy", "bool", true, "bad_request", "invalid input: closed_reopen_policy has the wrong type"},
		{"closed_reopen_policy", "unknown", "bogus", "invalid_closed_reopen_policy", "closed reopen policy must be one of: off, admin, staff_admin"},
		{"closed_reopen_policy", "empty", "", "invalid_closed_reopen_policy", "closed reopen policy must be one of: off, admin, staff_admin"},
		{"audit_mask_requester_names", "null", nil, "bad_request", "invalid input: audit_mask_requester_names must not be null"},
		{"audit_mask_requester_names", "number", 42, "bad_request", "invalid input: audit_mask_requester_names has the wrong type"},
		{"audit_mask_requester_names", "bool", true, "bad_request", "invalid input: audit_mask_requester_names has the wrong type"},
		{"audit_mask_requester_names", "unknown", "bogus", "invalid_audit_mask_requester_names", "audit_mask_requester_names must be one of: admin_log, ticket_log, everywhere"},
		{"audit_mask_requester_names", "empty", "", "invalid_audit_mask_requester_names", "audit_mask_requester_names must be one of: admin_log, ticket_log, everywhere"},
		{"attachment_scan_policy", "null", nil, "invalid_scan_policy", "scan policy must be one of: off, required, permissive"},
		{"attachment_scan_policy", "number", 42, "bad_request", "scan policy must be a string"},
		{"attachment_scan_policy", "bool", true, "bad_request", "scan policy must be a string"},
		{"attachment_scan_policy", "unknown", "bogus", "invalid_scan_policy", "scan policy must be one of: off, required, permissive"},
		{"attachment_scan_policy", "empty", "", "invalid_scan_policy", "scan policy must be one of: off, required, permissive"},
		{"attachment_infected_handling", "null", nil, "invalid_infected_handling", "infected attachment handling must be one of: refuse, quarantine"},
		{"attachment_infected_handling", "number", 42, "bad_request", "infected attachment handling must be a string"},
		{"attachment_infected_handling", "bool", true, "bad_request", "infected attachment handling must be a string"},
		{"attachment_infected_handling", "unknown", "bogus", "invalid_infected_handling", "infected attachment handling must be one of: refuse, quarantine"},
		{"attachment_infected_handling", "empty", "", "invalid_infected_handling", "infected attachment handling must be one of: refuse, quarantine"},
		{"attachment_mismatch_handling", "null", nil, "invalid_mismatch_handling", "mismatched attachment handling must be one of: refuse, wrap"},
		{"attachment_mismatch_handling", "number", 42, "bad_request", "mismatched attachment handling must be a string"},
		{"attachment_mismatch_handling", "bool", true, "bad_request", "mismatched attachment handling must be a string"},
		{"attachment_mismatch_handling", "unknown", "bogus", "invalid_mismatch_handling", "mismatched attachment handling must be one of: refuse, wrap"},
		{"attachment_mismatch_handling", "empty", "", "invalid_mismatch_handling", "mismatched attachment handling must be one of: refuse, wrap"},
		{"attachment_reputation_refresh", "null", nil, "invalid_reputation_refresh", "reputation refresh must be one of: weekly, biweekly, monthly, quarterly, never"},
		{"attachment_reputation_refresh", "number", 42, "bad_request", "reputation refresh must be a string"},
		{"attachment_reputation_refresh", "bool", true, "bad_request", "reputation refresh must be a string"},
		{"attachment_reputation_refresh", "unknown", "bogus", "invalid_reputation_refresh", "reputation refresh must be one of: weekly, biweekly, monthly, quarterly, never"},
		{"attachment_reputation_refresh", "empty", "", "invalid_reputation_refresh", "reputation refresh must be one of: weekly, biweekly, monthly, quarterly, never"},
	}
	for _, c := range cases {
		t.Run(c.key+"/"+c.name, func(t *testing.T) {
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{c.key: c.value})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			var got struct {
				Error struct{ Code, Message string }
			}
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, c.code, got.Error.Code)
			require.Equal(t, c.msg, got.Error.Message)
		})
	}
	// A valid value is still accepted.
	for _, ck := range choiceKeys {
		for _, v := range ck.valid {
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{ck.key: v})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "%s=%s body: %s", ck.key, v, body)
		}
	}
}

// #383: the dump reports each choice-list setting as the value in force, so a
// damaged stored value comes back as what the server actually enforces.
func TestSettings_DumpReportsEveryChoiceSettingInForce(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	for _, ck := range choiceKeys {
		t.Run(ck.key, func(t *testing.T) {
			t.Run("unset stays absent", func(t *testing.T) {
				require.NotContains(t, settingsDump(t, sess), ck.key)
			})
			for _, v := range ck.valid {
				t.Run("valid "+v, func(t *testing.T) {
					require.NoError(t, h.adminSvc.SetString(ctx, ck.key, v))
					got := settingsDump(t, sess)
					require.Contains(t, got, ck.key)
					require.JSONEq(t, `"`+v+`"`, string(got[ck.key]))
				})
			}
			// The uppercase of a real value is damaged too: the readers compare exactly.
			damaged := append([]string{`"` + strings.ToUpper(ck.valid[0]) + `"`}, damagedChoiceValues...)
			for _, stored := range damaged {
				t.Run("damaged "+stored, func(t *testing.T) {
					require.NoError(t, h.adminSvc.SetRaw(ctx, ck.key, []byte(stored)))
					got := settingsDump(t, sess)
					if ck.reader == nil {
						require.NotContains(t, got, ck.key)
						return
					}
					want, err := json.Marshal(ck.reader(h, ctx))
					require.NoError(t, err)
					require.JSONEq(t, string(want), string(got[ck.key]),
						"the dump must report what the reader enforces")
				})
			}
		})
	}
}

// The scan policy's in-force value depends on whether a scanner address is
// configured, so a damaged one is reported as unset rather than as today's
// answer, which the page would then save.
func TestSettings_DamagedScanPolicyIsReportedUnsetWhateverTheAddress(t *testing.T) {
	for _, addr := range []string{"", "tcp://127.0.0.1:1"} {
		t.Run("address "+addr, func(t *testing.T) {
			h, cleanup := newHarnessWith(t, 0, addr)
			defer cleanup()
			ctx := context.Background()
			sess := adminSession(t, h)
			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentScanPolicy, []byte(`"bogus"`)))
			require.NotContains(t, settingsDump(t, sess), admin.KeyAttachmentScanPolicy)
		})
	}
}

// #383: the settings page sends the whole dump back on every save. A damaged
// value in one choice-list setting must not block a save of anything else.
func TestSettings_DamagedChoiceSettingDoesNotBlockOtherSaves(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := adminSession(t, h)

	// What SettingsPage.tsx does: the dump, minus the "_set" flags, plus an edit.
	saveDumpWithSiteName := func(t *testing.T, name string) {
		t.Helper()
		patch := map[string]any{}
		for k, v := range settingsDump(t, sess) {
			if !strings.HasSuffix(k, "_set") {
				patch[k] = v
			}
		}
		patch[admin.KeySiteName] = name
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", patch)
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
		require.Equal(t, name, h.adminSvc.SiteName(ctx))
	}

	t.Run("baseline: an undamaged dump saves", func(t *testing.T) {
		saveDumpWithSiteName(t, "Baseline")
	})

	for _, ck := range choiceKeys {
		t.Run(ck.key, func(t *testing.T) {
			require.NoError(t, h.adminSvc.SetRaw(ctx, ck.key, []byte(`"bogus"`)))
			saveDumpWithSiteName(t, "Saved past "+ck.key)

			// And the PATCH itself still refuses the damaged value.
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{ck.key: "bogus"})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

			// Leave it valid so the next key's save is about that key alone.
			require.NoError(t, h.adminSvc.SetString(ctx, ck.key, ck.valid[0]))
		})
	}
}

// A request with two bad values names whichever the handler checks first.
// That precedence is what clients see, so it is recorded here from the code
// before the shared table existed, and the table must not reorder it: the six
// choice checks sit in the handler at their original positions, with the
// non-choice checks between them.
func TestSettings_PatchRefusalPrecedenceIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	bad := "bogus"
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"scan + closed", map[string]any{admin.KeyAttachmentScanPolicy: bad, admin.KeyClosedReopenPolicy: bad}, "invalid_closed_reopen_policy"},
		{"infected + closed", map[string]any{admin.KeyAttachmentInfectedHandling: bad, admin.KeyClosedReopenPolicy: bad}, "invalid_closed_reopen_policy"},
		{"refresh + mask", map[string]any{admin.KeyAttachmentReputationRefresh: bad, admin.KeyAuditMaskRequesterNames: bad}, "invalid_audit_mask_requester_names"},
		{"mask + closed", map[string]any{admin.KeyAuditMaskRequesterNames: bad, admin.KeyClosedReopenPolicy: bad}, "invalid_closed_reopen_policy"},
		{"scan + infected", map[string]any{admin.KeyAttachmentScanPolicy: bad, admin.KeyAttachmentInfectedHandling: bad}, "invalid_scan_policy"},
		{"mismatch + infected", map[string]any{admin.KeyAttachmentMismatchHandling: bad, admin.KeyAttachmentInfectedHandling: bad}, "invalid_infected_handling"},
		{"refresh + mismatch", map[string]any{admin.KeyAttachmentReputationRefresh: bad, admin.KeyAttachmentMismatchHandling: bad}, "invalid_mismatch_handling"},
		{"infected + bad scanner address", map[string]any{admin.KeyAttachmentInfectedHandling: bad, admin.KeyAttachmentScanAddress: "not an address"}, "invalid_scanner_address"},
		{"refresh + bad reputation toggle", map[string]any{admin.KeyAttachmentReputationRefresh: bad, admin.KeyAttachmentReputationVirusTotalEnabled: "x"}, "invalid_reputation_config"},
		{"scan + bad change-history toggle", map[string]any{admin.KeyAttachmentScanPolicy: bad, admin.KeyStaffCanViewTicketChangeHistory: "x"}, "bad_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Repeated, because the old map-order loop was random.
			for i := 0; i < 20; i++ {
				res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", c.body)
				require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
				var got struct{ Error struct{ Code string } }
				require.NoError(t, json.Unmarshal(body, &got))
				require.Equal(t, c.code, got.Error.Code)
			}
		})
	}
}
