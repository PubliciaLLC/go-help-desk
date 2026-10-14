package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// GET /auth/providers says what the login page can use right now, not what a
// setting claims (#421). The old answer was the raw oidc_enabled setting, so a
// provider that never loaded still got a button that led to a 503.

type providersBody struct {
	Providers []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	} `json:"providers"`
	LocalAdminOnly bool `json:"local_admin_only"`
}

func (p providersBody) enabled(name string) bool {
	for _, e := range p.Providers {
		if e.Name == name {
			return e.Enabled
		}
	}
	return false
}

func getProviders(t *testing.T, h *harness) (*http.Response, providersBody) {
	t.Helper()
	resp := h.doUnauth(t, http.MethodGet, "/api/v1/auth/providers", nil)
	var out providersBody
	decodeJSON(t, resp, &out)
	return resp, out
}

func TestAuthProviders_IsServedAsJSON(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	resp, _ := getProviders(t, h)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestAuthProviders_OIDCMeansLoaded(t *testing.T) {
	t.Run("saved and switched on but never loaded", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		require.NoError(t, h.adminSvc.SetOIDCConfig(context.Background(), auth.OIDCConfig{
			Enabled: true, IssuerURL: "https://idp.invalid", ClientID: "c", ClientSecret: "s",
			RedirectURL: "http://app.test/cb",
		}))
		_, got := getProviders(t, h)
		require.False(t, got.enabled("oidc"))
		require.True(t, got.enabled("local"), "local stays true")
	})
	t.Run("loaded", func(t *testing.T) {
		oh, cleanup := newOIDCHarness(t)
		defer cleanup()
		_, got := getProviders(t, oh.harness)
		require.True(t, got.enabled("oidc"))
	})
}

func TestAuthProviders_SAMLMeansLoaded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, got := getProviders(t, h)
	require.False(t, got.enabled("saml"), "nothing loaded")

	// saml_enabled is a different question (who may use a password), and
	// turning it on does not make a provider that is not loaded usable.
	require.NoError(t, h.adminSvc.SetSAMLEnabled(ctx, true))
	_, got = getProviders(t, h)
	require.False(t, got.enabled("saml"))
	require.True(t, got.LocalAdminOnly)
	require.NoError(t, h.adminSvc.SetSAMLEnabled(ctx, false))

	// Loaded with saml_enabled off: GET /auth/saml/login works, so say so.
	sess := signInAsAdmin(t, h)
	idp := newFakeSAMLIdP(t)
	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": idp.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	_, got = getProviders(t, h)
	require.True(t, got.enabled("saml"))
	require.False(t, got.LocalAdminOnly)
}

// local stays true whatever saml_enabled says, as it always has; the new field
// is what tells a client that password sign-in is limited to administrators.
func TestAuthProviders_LocalAdminOnlyFollowsSAMLEnabled(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, got := getProviders(t, h)
	require.True(t, got.enabled("local"))
	require.False(t, got.LocalAdminOnly)

	require.NoError(t, h.adminSvc.SetSAMLEnabled(ctx, true))
	_, got = getProviders(t, h)
	require.True(t, got.enabled("local"))
	require.True(t, got.LocalAdminOnly)
}
