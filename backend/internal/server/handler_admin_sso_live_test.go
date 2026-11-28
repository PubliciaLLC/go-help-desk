package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// GET /admin/oidc and /admin/saml report two different facts. `configured` is
// "the saved settings are complete"; `live` is "that provider is loaded and
// can sign someone in right now". A saved address that never answered used to
// look the same as a working one (#421).

func getSSO(t *testing.T, h *harness, path string) map[string]any {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]any
	decodeJSON(t, resp, &out)
	return out
}

func TestAdminOIDC_LiveFollowsTheLoadedProvider(t *testing.T) {
	t.Run("saved but not loaded", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetOIDCConfig(context.Background(), auth.OIDCConfig{
			Enabled: true, IssuerURL: "https://idp.invalid", ClientID: "c", ClientSecret: "s",
			RedirectURL: "http://app.test/cb",
		}))
		got := getSSO(t, h, "/api/v1/admin/oidc")
		require.Equal(t, true, got["configured"])
		require.Equal(t, false, got["live"])
	})
	t.Run("loaded", func(t *testing.T) {
		oh, cleanup := newOIDCHarness(t)
		defer cleanup()
		got := getSSO(t, oh.harness, "/api/v1/admin/oidc")
		require.Equal(t, true, got["configured"])
		require.Equal(t, true, got["live"])
	})
	t.Run("nothing saved", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		got := getSSO(t, h, "/api/v1/admin/oidc")
		require.Equal(t, false, got["configured"])
		require.Equal(t, false, got["live"])
	})
}

func TestAdminSAML_LiveFollowsTheLoadedMiddleware(t *testing.T) {
	t.Run("saved but not loaded", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetSAMLConfig(context.Background(),
			"https://idp.invalid/metadata", validSAMLCert, validSAMLKey))
		got := getSSO(t, h, "/api/v1/admin/saml")
		require.Equal(t, true, got["configured"])
		require.Equal(t, false, got["live"])
	})
	t.Run("loaded", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		sess := signInAsAdmin(t, h)
		idp := newFakeSAMLIdP(t)
		res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
			"metadata_url": idp.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
		})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
		got := getSSO(t, h, "/api/v1/admin/saml")
		require.Equal(t, true, got["configured"])
		require.Equal(t, true, got["live"])
	})
	t.Run("nothing saved", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		got := getSSO(t, h, "/api/v1/admin/saml")
		require.Equal(t, false, got["configured"])
		require.Equal(t, false, got["live"])
	})
}
