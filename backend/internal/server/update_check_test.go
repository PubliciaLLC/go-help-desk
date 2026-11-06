package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/updatecheck"
	"github.com/publiciallc/go-help-desk/backend/internal/version"
)

// #170: the opt-in weekly check for new releases, as the server exposes it.
// The checker itself is tested in internal/updatecheck against a fake GitHub.

const (
	settingsPath = "/api/v1/admin/settings"
	warningsPath = "/api/v1/admin/security-warnings"
	setupPath    = "/api/v1/setup"
)

func (h *harness) settings(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	res := h.doAsAdminSession(t, http.MethodGet, settingsPath, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out map[string]json.RawMessage
	decodeJSON(t, res, &out)
	return out
}

func setupBody(extra map[string]any) map[string]any {
	b := map[string]any{"email": "admin@example.com", "display_name": "Admin", "password": "correct-horse-battery"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func TestSetup_StoresTheUpdateCheckChoice(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]any
		want  string
	}{
		"ticked":    {map[string]any{"update_check": true}, "true"},
		"unticked":  {map[string]any{"update_check": false}, "false"},
		"not asked": {nil, "false"}, // an older client: off, never on
	} {
		t.Run(name, func(t *testing.T) {
			h, cleanup := newBareHarness(t)
			defer cleanup()
			c := setupAndSignIn(t, h, setupBody(tc.extra))

			res := h.doUnauthWithCookie(t, http.MethodGet, settingsPath, c)
			defer res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode)
			var got map[string]json.RawMessage
			decodeJSON(t, res, &got)
			require.Equal(t, tc.want, string(got[admin.KeyUpdateCheckEnabled]))
			// Setup asked the question, so the post-upgrade wizard must not.
			require.Equal(t, "true", string(got[admin.KeyPostUpgrade130Seen]))
		})
	}
}

func TestUpgradedInstall_HasNotBeenAsked(t *testing.T) {
	// An install that never ran 1.3.0's setup has no seen flag, and the check
	// is off. This is what the wizard keys on.
	h, cleanup := newHarness(t)
	defer cleanup()
	got := h.settings(t)
	require.NotContains(t, got, admin.KeyPostUpgrade130Seen)
	require.NotEqual(t, "true", string(got[admin.KeyUpdateCheckEnabled]))
}

func TestSettings_SeenFlagAndCheckRoundTrip(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdminSession(t, http.MethodPatch, settingsPath,
		map[string]any{admin.KeyUpdateCheckEnabled: true, admin.KeyPostUpgrade130Seen: true})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	got := h.settings(t)
	require.Equal(t, "true", string(got[admin.KeyUpdateCheckEnabled]))
	require.Equal(t, "true", string(got[admin.KeyPostUpgrade130Seen]))
}

func TestSettings_UpdateCheckNeedsASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodPatch, settingsPath, map[string]any{admin.KeyUpdateCheckEnabled: true})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "an API key must not switch on a request to a third party")
}

func TestSettings_TheCheckRecordIsServerKept(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyUpdateCheckResult, []byte(`{"latest":"9.9.9"}`)))

	require.NotContains(t, h.settings(t), admin.KeyUpdateCheckResult, "the page sends the dump back on save")
	res := h.doAsAdminSession(t, http.MethodPatch, settingsPath, map[string]any{admin.KeyUpdateCheckResult: "{}"})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func (h *harness) warnings(t *testing.T, as who) (int, map[string]json.RawMessage) {
	t.Helper()
	res := h.doSession(t, h.session(t, as), http.MethodGet, warningsPath, nil)
	defer res.Body.Close()
	var out map[string]json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (h *harness) recordRelease(t *testing.T, enabled bool, latest string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyUpdateCheckEnabled, enabled))
	raw, _ := json.Marshal(updatecheck.Record{CheckedAt: time.Now(), Latest: latest})
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyUpdateCheckResult, raw))
}

func TestSecurityWarnings_UpdateNotice(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	status, got := h.warnings(t, asAdmin)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, got, "update", "nothing recorded, nothing shown")

	h.recordRelease(t, true, "99.0.0")
	_, got = h.warnings(t, asAdmin)
	var n struct{ Current, Latest, URL string }
	require.NoError(t, json.Unmarshal(got["update"], &n))
	require.Equal(t, version.Version, n.Current)
	require.Equal(t, "99.0.0", n.Latest)
	require.Equal(t, "https://github.com/PubliciaLLC/go-help-desk/releases/tag/v99.0.0", n.URL)

	h.recordRelease(t, true, "0.0.1")
	_, got = h.warnings(t, asAdmin)
	require.NotContains(t, got, "update", "an older release is not news")

	h.recordRelease(t, false, "99.0.0")
	_, got = h.warnings(t, asAdmin)
	require.NotContains(t, got, "update", "turning the check off hides the notice")
}

func TestSecurityWarnings_UpdateNoticeIsAdminOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.recordRelease(t, true, "99.0.0")
	for _, as := range []who{asStaff, asReporter} {
		status, got := h.warnings(t, as)
		require.Equal(t, http.StatusForbidden, status)
		require.NotContains(t, got, "update")
	}
}

func TestPostUpgrade_CountsAccountsThatWouldNeedAReset(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	// The harness hashes at bcrypt's minimum (4); raise the target to 6 so the
	// seeded accounts are old hashes. The administrator is one too, and exempt.
	withExpiry(t, h, 0)
	current := "$2a$06$" + "abcdefghijklmnopqrstuvabcdefghijklmnopqrstuvabcdefghi"
	set := func(id any, hash string) {
		_, err := h.tx.ExecContext(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash)
		require.NoError(t, err)
	}

	count := func() int {
		res := h.doAsAdminSession(t, http.MethodGet, "/api/v1/admin/post-upgrade", nil)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var out struct {
			OldPasswordAccounts int `json:"old_password_accounts"`
		}
		decodeJSON(t, res, &out)
		return out.OldPasswordAccounts
	}
	require.Equal(t, 2, count())
	set(h.userID, current)
	require.Equal(t, 1, count())
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET disabled = true WHERE id = $1`, h.staffID)
	require.NoError(t, err)
	require.Equal(t, 0, count(), "a disabled account is not waiting on anything")

	res := h.doSession(t, h.session(t, asReporter), http.MethodGet, "/api/v1/admin/post-upgrade", nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}

// Both new flags are booleans and nothing else. Without a check, "true", 1, an
// object and null all answered 204 and then read back as off, so the operator
// believed they had chosen.
func TestSettings_UpdateKeysRefuseNonBooleans(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, key := range []string{admin.KeyUpdateCheckEnabled, admin.KeyPostUpgrade130Seen} {
		for name, val := range map[string]any{
			"string": "true", "null": nil, "number": 1, "object": map[string]any{"a": true}, "array": []any{true},
		} {
			t.Run(key+"/"+name, func(t *testing.T) {
				// A valid key alongside it: the body is refused whole.
				res := h.doAsAdminSession(t, http.MethodPatch, settingsPath,
					map[string]any{key: val, admin.KeySiteName: "Should Not Be Stored"})
				res.Body.Close()
				require.Equal(t, http.StatusBadRequest, res.StatusCode)

				got := h.settings(t)
				require.NotContains(t, got, key, "nothing stored")
				require.NotEqual(t, `"Should Not Be Stored"`, string(got[admin.KeySiteName]))
			})
		}
	}
}
