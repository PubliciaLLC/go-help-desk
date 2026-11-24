package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// saml_enabled means "staff and users must use SAML". Saved while SAML is not
// loaded, it leaves them with no way in and no SAML button. That is the
// administrator's to choose, so the save succeeds, and says so.

const samlNotLoaded = "SAML is not loaded"

func warningOf(t *testing.T, status int, body []byte) string {
	t.Helper()
	if status == http.StatusNoContent {
		return ""
	}
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	var out struct {
		Warning string `json:"warning"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	return out.Warning
}

func loadSAML(t *testing.T, h *harness, sess *session) {
	t.Helper()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": idp.srv.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/saml")["live"], "precondition")
}

func enableSAML(t *testing.T, sess *session) {
	t.Helper()
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	// 200 with a warning when SAML is not loaded yet: that is not what is tested here.
	require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, res.StatusCode, "body: %s", body)
}

// PATCH /admin/settings: the warning is about the state after the save and the
// reload, said when the body set the switch or a SAML field.
func TestPatchSettings_SAMLEnabledWithoutLiveSAMLWarns(t *testing.T) {
	cases := []struct {
		name string
		// live: SAML is loaded before the request. enabled: saml_enabled is on before it.
		live, enabled bool
		body          func(t *testing.T, h *harness) map[string]any
		wantWarning   bool
	}{
		{
			name:        "switch on, SAML not loaded",
			body:        func(*testing.T, *harness) map[string]any { return map[string]any{"saml_enabled": true} },
			wantWarning: true,
		},
		{
			name: "switch on, SAML loaded",
			live: true,
			body: func(*testing.T, *harness) map[string]any { return map[string]any{"saml_enabled": true} },
		},
		{
			name: "switch off, SAML not loaded",
			body: func(*testing.T, *harness) map[string]any { return map[string]any{"saml_enabled": false} },
		},
		{
			// The check runs after the reload, so a body that brings SAML up
			// while turning the switch on is not warned about.
			name: "switch on together with a working SAML configuration",
			body: func(t *testing.T, h *harness) map[string]any {
				idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
				return map[string]any{
					"saml_enabled": true, "saml_metadata_url": idp.srv.URL,
					"saml_cert_pem": validSAMLCert, "saml_key_pem": validSAMLKey,
				}
			},
		},
		{
			name:        "SAML removed while the switch is on",
			live:        true,
			enabled:     true,
			body:        func(*testing.T, *harness) map[string]any { return map[string]any{"saml_key_pem": ""} },
			wantWarning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			if tc.live {
				loadSAML(t, h, sess)
			}
			if tc.enabled {
				enableSAML(t, sess)
			}
			res, resBody := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", tc.body(t, h))
			w := warningOf(t, res.StatusCode, resBody)
			if tc.wantWarning {
				require.Contains(t, w, samlNotLoaded)
				require.Contains(t, w, "staff and users")
			} else {
				require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", resBody)
			}
		})
	}
}

// A warning, not a refusal: the setting was saved.
func TestPatchSettings_SAMLEnabledWithoutLiveSAMLIsStillSaved(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	require.Contains(t, warningOf(t, res.StatusCode, body), samlNotLoaded)
	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/settings")["saml_enabled"])
}

// PUT /admin/saml says the same when its own save leaves SAML not loaded.
func TestSaveSAMLConfig_SAMLEnabledWarning(t *testing.T) {
	cases := []struct {
		name string
		live bool
		body map[string]any
		// wantAll must all be in the warning; empty means no warning at all.
		wantAll []string
	}{
		{
			name:    "everything removed",
			live:    true,
			body:    map[string]any{"metadata_url": "", "metadata_xml": "", "cert_pem": "", "key_pem": ""},
			wantAll: []string{samlNotLoaded},
		},
		{
			// The metadata cannot be fetched, so nothing loads: both sentences.
			name: "unreachable metadata with a valid pair",
			body: map[string]any{
				"metadata_url": "https://idp.test/metadata", "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantAll: []string{"could not be loaded", samlNotLoaded},
		},
		{
			name: "a change that leaves SAML loaded",
			live: true,
			body: map[string]any{"cert_pem": validSAMLCert},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			if tc.live {
				loadSAML(t, h, sess)
			}
			enableSAML(t, sess)

			res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", tc.body)
			w := warningOf(t, res.StatusCode, body)
			if len(tc.wantAll) == 0 {
				require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
			}
			for _, want := range tc.wantAll {
				require.Contains(t, w, want)
			}
		})
	}
}

// The page shows what to give the identity provider; the server builds both
// from its base URL.
func TestGetSAMLConfig_GivesEntityIDAndACSURL(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	got := adminGet(t, sess, "/api/v1/admin/saml")
	require.Equal(t, h.cfgBaseURL()+"/api/v1/auth/saml/metadata", got["sp_metadata_url"])
	require.Equal(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs", got["sp_acs_url"])
}
