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

func TestPatchSettings_SAMLEnabledWithoutLiveSAMLWarns(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	require.Equal(t, false, adminGet(t, sess, "/api/v1/admin/saml")["live"], "precondition")

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	w := warningOf(t, res.StatusCode, body)
	require.Contains(t, w, "SAML is not loaded")
	require.Contains(t, w, "staff and users")

	// It is a warning, not a refusal: the setting was saved.
	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/settings")["saml_enabled"])
}

func TestPatchSettings_SAMLEnabledWithLiveSAMLDoesNotWarn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	loadSAML(t, h, sess)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}

func TestPatchSettings_SAMLEnabledFalseDoesNotWarn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": false})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}

// The same state reached from the other side: the switch is already on and the
// SAML configuration is taken away.
func TestPatchSettings_RemovingSAMLWhileSAMLEnabledWarns(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	loadSAML(t, h, sess)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_key_pem": ""})
	require.Contains(t, warningOf(t, res.StatusCode, body), "SAML is not loaded")
}

func TestSaveSAMLConfig_RemovingSAMLWhileSAMLEnabledWarns(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	loadSAML(t, h, sess)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "", "metadata_xml": "", "cert_pem": "", "key_pem": "",
	})
	require.Contains(t, warningOf(t, res.StatusCode, body), "SAML is not loaded")
}

// An edit to a SAML field that leaves SAML loaded is not news.
func TestSaveSAMLConfig_ChangingLiveSAMLWhileSAMLEnabledDoesNotWarn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	loadSAML(t, h, sess)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_enabled": true})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{"cert_pem": validSAMLCert})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
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
