package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// A reload reads the stored settings, builds from them (a network fetch of up to
// 15 seconds) and commits. Without one lock across all three, a slow older
// reload commits after a faster newer one and puts the old configuration back
// live, with the newer one saved in the database.
//
// Both tests hold the older reload inside its fetch, start the newer one, give
// it time to finish if nothing stops it, then release the older one.

// gatedServer answers handler after release is closed, and says when the first
// request arrived.
func gatedServer(t *testing.T, handler http.HandlerFunc) (srv *httptest.Server, started <-chan struct{}, release func()) {
	t.Helper()
	start := make(chan struct{})
	gate := make(chan struct{})
	var once, relOnce sync.Once
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(start) })
		<-gate
		handler(w, r)
	}))
	t.Cleanup(func() { relOnce.Do(func() { close(gate) }); srv.Close() })
	return srv, start, func() { relOnce.Do(func() { close(gate) }) }
}

func TestReloadSAML_ANewerReloadIsNotOverwrittenByAnOlderSlowOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp1 := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	idp2 := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	require.NotEqual(t, idp1.idp.SSOURL.String(), idp2.idp.SSOURL.String())

	slow, started, release := gatedServer(t, idp1.idp.ServeMetadata)
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, slow.URL, validSAMLCert, validSAMLKey))
	sess := adminSession(t, h)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.srv.InitSAML(ctx) }() // the older reload, stuck fetching idp1
	<-started
	var status int
	go func() { // the newer one: saves idp2 and reloads
		defer wg.Done()
		res, _ := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"saml_metadata_url": idp2.srv.URL})
		status = res.StatusCode
	}()
	time.Sleep(300 * time.Millisecond)
	release()
	wg.Wait()

	require.Contains(t, []int{http.StatusNoContent, http.StatusOK}, status)
	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.True(t, strings.HasPrefix(res.Header.Get("Location"), idp2.idp.SSOURL.String()),
		"the newer save is live, got %s", res.Header.Get("Location"))
}

func TestReloadOIDC_ANewerReloadIsNotOverwrittenByAnOlderSlowOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp2 := newFakeOIDC(t)

	var slowURL string
	slow, started, release := gatedServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                slowURL,
			"authorization_endpoint":                slowURL + "/authorize",
			"token_endpoint":                        slowURL + "/token",
			"jwks_uri":                              slowURL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	slowURL = slow.URL
	require.NoError(t, h.adminSvc.SetOIDCConfig(ctx, auth.OIDCConfig{
		Enabled: true, IssuerURL: slow.URL, ClientID: idp2.clientID, ClientSecret: idp2.clientSecret,
		RedirectURL: "http://app.test/api/v1/auth/oidc/callback",
	}))
	sess := adminSession(t, h)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = h.srv.InitOIDC(ctx) }() // the older reload, stuck in discovery
	<-started
	var status int
	go func() {
		defer wg.Done()
		res, _ := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"oidc_issuer_url": idp2.issuer()})
		status = res.StatusCode
	}()
	time.Sleep(300 * time.Millisecond)
	release()
	wg.Wait()

	require.Contains(t, []int{http.StatusNoContent, http.StatusOK}, status)
	res := h.rawGet(t, "/api/v1/auth/oidc/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.True(t, strings.HasPrefix(res.Header.Get("Location"), idp2.issuer()+"/authorize"),
		"the newer save is live, got %s", res.Header.Get("Location"))
}
