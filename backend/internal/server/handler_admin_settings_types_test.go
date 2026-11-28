package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every setting the PATCH accepts is type-checked, not just the ones added
// since #420. These ten took whatever JSON they were sent: "false" as a string
// reads back as off, null reads back as off, a negative reopen window reads
// back as a window that has already closed, and the administrator is told 204.
// The write is refused whole and the stored value is left as it was.
func TestSettings_TheOlderKeysAreTypeChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"mfa_enabled as a string", map[string]any{"mfa_enabled": "true"}},
		{"mfa_enabled null", map[string]any{"mfa_enabled": nil}},
		{"ticket_scope_enforced as a number", map[string]any{"ticket_scope_enforced": 1}},
		{"sla_enabled as a string", map[string]any{"sla_enabled": "yes"}},
		{"self_signup_enabled null", map[string]any{"self_signup_enabled": nil}},
		{"open_registration_enabled as a string", map[string]any{"open_registration_enabled": "true"}},
		{"saml_enabled as an object", map[string]any{"saml_enabled": map[string]any{}}},
		{"reopen_window_days as a string", map[string]any{"reopen_window_days": "7"}},
		{"reopen_window_days null", map[string]any{"reopen_window_days": nil}},
		{"reopen_window_days negative", map[string]any{"reopen_window_days": -1}},
		{"mfa_enforced_roles as a string", map[string]any{"mfa_enforced_roles": "admin"}},
		{"mfa_enforced_roles with a non-string entry", map[string]any{"mfa_enforced_roles": []any{1}}},
		{"mfa_enforced_roles null", map[string]any{"mfa_enforced_roles": nil}},
		{"reopen_target_status_name as a number", map[string]any{"reopen_target_status_name": 5}},
		{"reopen_target_status_name null", map[string]any{"reopen_target_status_name": nil}},
		{"site_logo_url as a number", map[string]any{"site_logo_url": 5}},
		{"site_logo_url null", map[string]any{"site_logo_url": nil}},
		{"site_logo_url javascript scheme", map[string]any{"site_logo_url": "javascript:alert(1)"}},
		{"site_logo_url data scheme", map[string]any{"site_logo_url": "data:image/png;base64,AAAA"}},
		{"site_logo_url file scheme", map[string]any{"site_logo_url": "file:///etc/passwd"}},
		{"site_logo_url scheme-relative", map[string]any{"site_logo_url": "//evil.example/logo.png"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)

			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", c.body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

			after, err := h.adminSvc.ListAll(context.Background())
			require.NoError(t, err)
			for k := range c.body {
				require.Equal(t, string(before[k]), string(after[k]), "%s must be left as it was", k)
			}
		})
	}
}

// The other half: what the settings page and the logo upload really store must
// still be accepted, or the check would break every save.
func TestSettings_TheOlderKeysStillAcceptTheirRealValues(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled":               false,
		"ticket_scope_enforced":     false,
		"sla_enabled":               true,
		"self_signup_enabled":       false,
		"open_registration_enabled": false,
		"saml_enabled":              false,
		"reopen_window_days":        0,
		"mfa_enforced_roles":        []string{"admin"},
		"reopen_target_status_name": "New",
		// The logo upload stores a path on this origin, and the settings page
		// sends the whole object back.
		"site_logo_url": "/api/v1/logo?v=1700000000",
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	for _, logo := range []string{"", "https://cdn.example.com/logo.png", "http://intranet.example/logo.png"} {
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"site_logo_url": logo})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "%q: %s", logo, body)
	}
}
