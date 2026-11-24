package server_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// saml_enabled ("Require SAML for staff and users") on while SAML is not loaded
// strands every staff and user account, and the save-time warning is said only
// once. So GET /admin/security-warnings carries it for as long as it is true
// (docs/DESIGN.md, SAML, "A server problem while saml_enabled is on and SAML is
// not loaded").

// samlRequiredNotLoaded returns the field as the endpoint sends it: nil when it
// is absent, never a JSON null.
func samlRequiredNotLoaded(t *testing.T, sess *session) map[string]any {
	t.Helper()
	got := adminGet(t, sess, "/api/v1/admin/security-warnings")
	v, present := got["saml_required_not_loaded"]
	if !present {
		return nil
	}
	obj, ok := v.(map[string]any)
	require.True(t, ok, "saml_required_not_loaded must be an object when present, got %#v", v)
	return obj
}

func TestGetSecurityWarnings_SAMLRequiredNotLoaded(t *testing.T) {
	cases := []struct {
		name string
		// live: SAML is loaded. enabled: saml_enabled is on. setup runs before
		// the switch is turned on.
		live, enabled bool
		setup         func(t *testing.T, sess *session)
		// wantReason is "" when the field must be absent.
		wantReason string
	}{
		{name: "enabled, nothing saved", enabled: true, wantReason: "incomplete"},
		{
			name: "enabled, the key is missing", enabled: true, wantReason: "incomplete",
			setup: func(t *testing.T, sess *session) {
				res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
					"saml_metadata_url": "https://idp.test/metadata", "saml_cert_pem": validSAMLCert,
				})
				require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, res.StatusCode, "body: %s", body)
			},
		},
		{
			// Everything saved, but the metadata cannot be fetched, so SAML did not load.
			name: "enabled, metadata fails to load", enabled: true, wantReason: "not_loaded",
			setup: func(t *testing.T, sess *session) {
				res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
					"metadata_url": "https://idp.test/metadata", "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
				})
				require.Equal(t, http.StatusOK, res.StatusCode, "a save that did not load answers a warning; body: %s", body)
			},
		},
		{name: "enabled and loaded", live: true, enabled: true},
		{name: "disabled, nothing saved"},
		{name: "disabled and loaded", live: true},
		{
			// A half-filled form with the switch off is an operator part-way through.
			name: "disabled, half configured",
			setup: func(t *testing.T, sess *session) {
				res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_cert_pem": validSAMLCert})
				require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, res.StatusCode, "body: %s", body)
			},
		},
		{
			name: "disabled, metadata fails to load",
			setup: func(t *testing.T, sess *session) {
				res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
					"metadata_url": "https://idp.test/metadata", "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
				})
				require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
			},
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
			if tc.setup != nil {
				tc.setup(t, sess)
			}
			if tc.enabled {
				enableSAML(t, sess)
			}

			got := samlRequiredNotLoaded(t, sess)
			if tc.wantReason == "" {
				require.Nil(t, got)
				return
			}
			require.Equal(t, map[string]any{"reason": tc.wantReason}, got)
		})
	}
}

// Nothing is remembered: the problem is read from live state, so it comes and
// goes with the switch and with SAML loading, with no restart.
func TestGetSecurityWarnings_SAMLRequiredNotLoadedClearsByItself(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	enableSAML(t, sess)
	require.Equal(t, "incomplete", samlRequiredNotLoaded(t, sess)["reason"], "the switch is on and SAML is not set up")

	loadSAML(t, h, sess)
	require.Nil(t, samlRequiredNotLoaded(t, sess), "SAML loaded")

	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{"metadata_url": "", "metadata_xml": "", "cert_pem": "", "key_pem": ""})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Equal(t, "incomplete", samlRequiredNotLoaded(t, sess)["reason"], "SAML removed while the switch is on")

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": false})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Nil(t, samlRequiredNotLoaded(t, sess), "the switch is off")
}

// It says how sign-in is configured: administrators only, like the rest of the
// endpoint, and the field is left out of any other answer.
func TestGetSecurityWarnings_SAMLRequiredNotLoadedIsForAdministratorsOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	enableSAML(t, sess)
	require.NotNil(t, samlRequiredNotLoaded(t, sess), "precondition")

	for name, resp := range map[string]*http.Response{
		"anonymous": h.rawGet(t, "/api/v1/admin/security-warnings", nil),
		"requester": h.doAsUser(t, http.MethodGet, "/api/v1/admin/security-warnings", nil),
		"staff":     h.do(t, http.MethodGet, "/api/v1/admin/security-warnings", nil),
	} {
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode, name)
		require.NotContains(t, string(raw), "saml_required_not_loaded", name)
	}

	// The shape for the administrator: an object with a reason.
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		SAML *struct {
			Reason string `json:"reason"`
		} `json:"saml_required_not_loaded"`
	}
	decodeJSON(t, resp, &body)
	require.NotNil(t, body.SAML)
	require.Equal(t, "incomplete", body.SAML.Reason)
}
