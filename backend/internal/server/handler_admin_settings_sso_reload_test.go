package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// PATCH /admin/settings used to store the SAML and OIDC keys and leave the
// running provider as it was, so a saved change did nothing until a restart
// (#619). PUT /admin/saml and PUT /admin/oidc load what they save; the PATCH
// door does too now.

func adminGet(t *testing.T, s *session, path string) map[string]any {
	t.Helper()
	res, body := s.send(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func TestPatchSettings_ReloadsSAML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")

	require.Equal(t, false, adminGet(t, sess, "/api/v1/admin/saml")["live"], "precondition")

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"saml_metadata_url": idp.srv.URL, "saml_cert_pem": validSAMLCert, "saml_key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/saml")["live"],
		"the saved configuration is the running one")
	login := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	login.Body.Close()
	require.Equal(t, http.StatusFound, login.StatusCode)
}

// A change that cannot be loaded is saved, the running handler is kept, and the
// caller is told, in words that do not echo what the fetch said about whatever
// the URL pointed at.
func TestPatchSettings_SAMLReloadFailureWarnsAndKeepsTheLiveHandler(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"saml_metadata_url": idp.srv.URL, "saml_cert_pem": validSAMLCert, "saml_key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>internal admin console</html>"))
	}))
	defer html.Close()

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_metadata_url": html.URL})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var out struct {
		Warning string `json:"warning"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Contains(t, out.Warning, "SAML config saved, but the identity provider metadata could not be loaded")
	require.False(t, strings.Contains(string(body), "EntityDescriptor") || strings.Contains(string(body), html.URL),
		"the failure text is not echoed: %s", body)

	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/saml")["live"], "the working handler is kept")
	require.Equal(t, html.URL, adminGet(t, sess, "/api/v1/admin/saml")["metadata_url"], "and the change is saved")
}

// Blanking a field is a reload too: a handler that can no longer be built is
// cleared, as PUT does.
func TestPatchSettings_ClearingSAMLUnloadsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"saml_metadata_url": idp.srv.URL, "saml_cert_pem": validSAMLCert, "saml_key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/saml")["live"], "precondition")

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_key_pem": ""})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, false, adminGet(t, sess, "/api/v1/admin/saml")["live"])
}

func TestPatchSettings_ReloadsOIDC(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeOIDC(t)

	require.Equal(t, false, adminGet(t, sess, "/api/v1/admin/oidc")["live"], "precondition")

	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"oidc_enabled": true, "oidc_issuer_url": idp.issuer(),
		"oidc_client_id": idp.clientID, "oidc_client_secret": idp.clientSecret,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/oidc")["live"])

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"oidc_enabled": false})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, false, adminGet(t, sess, "/api/v1/admin/oidc")["live"], "turning it off unloads it")
}

func TestPatchSettings_OIDCReloadFailureWarnsAndKeepsTheLiveProvider(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeOIDC(t)
	res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"oidc_enabled": true, "oidc_issuer_url": idp.issuer(),
		"oidc_client_id": idp.clientID, "oidc_client_secret": idp.clientSecret,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	res, body = sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"oidc_issuer_url": deadURL})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var out struct {
		Warning string `json:"warning"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Contains(t, out.Warning, "OIDC config saved, but the identity provider could not be reached")
	require.NotContains(t, string(body), deadURL, "the failure text is not echoed")
	require.Equal(t, true, adminGet(t, sess, "/api/v1/admin/oidc")["live"], "the working provider is kept")
}
