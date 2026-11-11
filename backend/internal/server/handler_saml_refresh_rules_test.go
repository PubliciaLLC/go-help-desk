package server_test

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"
)

// The hourly SAML metadata refresh runs unattended, so what it fetches may not
// move the trust anchor on its own. These pin the rules: https only; the same
// EntityID; a changed signing-certificate set only when one certificate in use
// is still published (a rollover); anything else is refused, the running
// configuration kept, and an administrator told to re-save to accept it.

const movedSSO = "https://sso.moved.test/sso"

// refreshRig is a harness with SAML loaded from a metadata URL over TLS whose
// answer the test controls.
type refreshRig struct {
	h    *harness
	idp  *fakeIdP
	tls  *httptest.Server
	cur  *atomic.Pointer[saml.EntityDescriptor]
	sess *session
}

func newRefreshRig(t *testing.T) (*refreshRig, func()) {
	t.Helper()
	h, cleanup := newHarness(t)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	r := &refreshRig{h: h, idp: idp, cur: &atomic.Pointer[saml.EntityDescriptor]{}}
	r.cur.Store(idp.idp.Metadata())
	r.tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		b, err := xml.Marshal(r.cur.Load())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.tls.Close)
	h.srv.SetSAMLMetadataClientForTest(r.tls.Client())

	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, r.tls.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)
	require.Equal(t, idp.idp.SSOURL.String(), r.ssoPrefix(t), "precondition: loaded from the TLS URL")
	r.sess = adminSession(t, h)
	return r, cleanup
}

// ssoPrefix is the IdP endpoint the live middleware sends the browser to.
func (r *refreshRig) ssoPrefix(t *testing.T) string {
	t.Helper()
	res := r.h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc := res.Header.Get("Location")
	if i := strings.Index(loc, "?"); i >= 0 {
		loc = loc[:i]
	}
	return loc
}

// publish makes the metadata URL answer with md.
func (r *refreshRig) publish(md *saml.EntityDescriptor) { r.cur.Store(md) }

// moved is the original metadata with the sign-in endpoint moved, so a refresh
// that was applied is visible.
func (r *refreshRig) moved() *saml.EntityDescriptor {
	md := r.idp.idp.Metadata()
	for i := range md.IDPSSODescriptors {
		for j := range md.IDPSSODescriptors[i].SingleSignOnServices {
			md.IDPSSODescriptors[i].SingleSignOnServices[j].Location = movedSSO
		}
	}
	return md
}

// withExtraCert is moved() with a second signing certificate published beside
// the one in use: the shape of a rollover.
func (r *refreshRig) withExtraCert(t *testing.T) *saml.EntityDescriptor {
	t.Helper()
	md := r.moved()
	extra := idpMetadataExpiringAt(t, r.idp, time.Now().Add(400*24*time.Hour))
	md.IDPSSODescriptors[0].KeyDescriptors = append(md.IDPSSODescriptors[0].KeyDescriptors,
		extra.IDPSSODescriptors[0].KeyDescriptors...)
	return md
}

// replaced is moved() with the signing certificate swapped for another.
func (r *refreshRig) replaced(t *testing.T) *saml.EntityDescriptor {
	t.Helper()
	md := idpMetadataExpiringAt(t, r.idp, time.Now().Add(400*24*time.Hour))
	for i := range md.IDPSSODescriptors {
		for j := range md.IDPSSODescriptors[i].SingleSignOnServices {
			md.IDPSSODescriptors[i].SingleSignOnServices[j].Location = movedSSO
		}
	}
	return md
}

func (r *refreshRig) refreshProblem(t *testing.T) string {
	t.Helper()
	got := adminGet(t, r.sess, "/api/v1/admin/security-warnings")
	p, ok := got["saml_metadata_refresh"].(map[string]any)
	if !ok {
		return ""
	}
	return p["problem"].(string)
}

func TestRefreshSAML_AppliesARolloverThatKeepsACertificateInUse(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publish(r.withExtraCert(t))
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, movedSSO, r.ssoPrefix(t), "the rollover is live")
	require.Empty(t, r.refreshProblem(t))
}

func TestRefreshSAML_RefusesAFullReplacementOfTheSigningCertificate(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publish(r.replaced(t))
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "the running configuration is kept")
	require.Equal(t, "certificate_changed", r.refreshProblem(t))

	// An administrator accepts it by saving the settings again.
	res, body := r.sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": r.tls.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Contains(t, []int{http.StatusNoContent, http.StatusOK}, res.StatusCode, "body: %s", body)
	require.Equal(t, movedSSO, r.ssoPrefix(t), "the saved configuration is live")
	require.Empty(t, r.refreshProblem(t), "and the warning is gone")
}

func TestRefreshSAML_RefusesAChangedEntityID(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md := r.moved()
	md.EntityID = "https://some.other.idp/metadata"
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "the running configuration is kept")
	require.Equal(t, "entity_id_changed", r.refreshProblem(t))

	// The next good refresh clears it.
	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}

// An IdP that is down at the hourly tick changes nothing.
func TestRefreshSAML_KeepsTheLiveConfigurationWhenTheIdPIsDown(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publish(r.withExtraCert(t))
	r.tls.Close()
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t), "an outage is not a trust problem")
}

func TestRefreshSAML_DoesNothingForAnHTTPURLAndSaysWhy(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, idp.srv.URL, validSAMLCert, validSAMLKey))
	h.srv.InitSAML(ctx)
	sess := adminSession(t, h)

	var hits atomic.Int64
	idp.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		idp.idp.ServeMetadata(w, r)
	})
	moved, err := url.Parse(movedSSO)
	require.NoError(t, err)
	idp.idp.SSOURL = *moved
	h.srv.RefreshSAML(ctx)

	require.Zero(t, hits.Load(), "an http metadata URL is not fetched by the refresh")
	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.False(t, strings.HasPrefix(res.Header.Get("Location"), movedSSO))

	got := adminGet(t, sess, "/api/v1/admin/security-warnings")
	w, ok := got["saml_metadata_refresh"].(map[string]any)
	require.True(t, ok, "warnings: %v", got)
	require.Equal(t, "http_url", w["problem"])
}

// A change an administrator saved (on another replica) is theirs to make: it is
// loaded whatever it holds, with no overlap or EntityID rule, because what the
// refresh guards is the IdP's own answer changing under an unchanged setting.
func TestRefreshSAML_AppliesSettingsSavedElsewhere(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	// Same server, another path: the stored settings now differ from what the
	// running middleware was built from.
	r.publish(r.replaced(t))
	require.NoError(t, r.h.adminSvc.SetSAMLConfig(context.Background(), r.tls.URL+"/elsewhere", validSAMLCert, validSAMLKey))
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}
