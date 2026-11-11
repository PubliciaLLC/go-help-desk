package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// The hourly SAML metadata refresh runs unattended, so what it fetches may not
// move the trust anchor on its own. These pin the rules: https only; the same
// EntityID; and a changed signing-certificate set applied only when it is a
// subset of the one in use (certificates removed or unchanged). A refresh that
// ADDS a certificate is never applied, whether or not a certificate in use is
// still published beside it: the running configuration is kept, the new
// certificate's fingerprint and expiry are shown, and an administrator accepts
// it by saving the SAML settings again.

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

// withExtraCert is the original metadata (endpoint moved if movedEndpoint) with
// a second signing certificate published beside the one in use, and that
// certificate's descriptor.
func (r *refreshRig) withExtraCert(t *testing.T, movedEndpoint bool) (*saml.EntityDescriptor, saml.KeyDescriptor) {
	t.Helper()
	md := r.idp.idp.Metadata()
	if movedEndpoint {
		md = r.moved()
	}
	extra := idpMetadataExpiringAt(t, r.idp, time.Now().Add(400*24*time.Hour)).IDPSSODescriptors[0].KeyDescriptors[0]
	md.IDPSSODescriptors[0].KeyDescriptors = append(md.IDPSSODescriptors[0].KeyDescriptors, extra)
	return md, extra
}

// fingerprintOf is the SHA-256 fingerprint of the descriptor's certificate.
func fingerprintOf(t *testing.T, kd saml.KeyDescriptor) string {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(kd.KeyInfo.X509Data.X509Certificates[0].Data)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	return auth.Fingerprint(sum[:])
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

// refreshWarning is saml_metadata_refresh from security-warnings, or nil.
func (r *refreshRig) refreshWarning(t *testing.T) map[string]any {
	t.Helper()
	got := adminGet(t, r.sess, "/api/v1/admin/security-warnings")
	w, _ := got["saml_metadata_refresh"].(map[string]any)
	return w
}

func (r *refreshRig) refreshProblem(t *testing.T) string {
	t.Helper()
	if w := r.refreshWarning(t); w != nil {
		return w["problem"].(string)
	}
	return ""
}

// resave is the administrator's manual save of the same settings.
func (r *refreshRig) resave(t *testing.T) {
	t.Helper()
	res, body := r.sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": r.tls.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Contains(t, []int{http.StatusNoContent, http.StatusOK}, res.StatusCode, "body: %s", body)
}

func (r *refreshRig) requireNewCertWarning(t *testing.T, kd saml.KeyDescriptor) {
	t.Helper()
	w := r.refreshWarning(t)
	require.NotNil(t, w, "a warning is raised")
	require.Equal(t, "new_certificate", w["problem"])
	certs, ok := w["new_certificates"].([]any)
	require.True(t, ok, "warning: %v", w)
	require.Len(t, certs, 1)
	c := certs[0].(map[string]any)
	require.Equal(t, fingerprintOf(t, kd), c["sha256"], "the fingerprint to compare with the provider's")
	exp, err := time.Parse(time.RFC3339, c["expires_at"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(400*24*time.Hour), exp, 2*time.Minute)
}

// A certificate added beside the one in use is what an attacker serving the
// response would do (keep the real one, add their own), so it is not applied.
func TestRefreshSAML_DoesNotApplyACertificateAddedBesideTheOneInUse(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md, extra := r.withExtraCert(t, true)
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "nothing from the refresh is applied, the endpoint included")
	r.requireNewCertWarning(t, extra)

	// The administrator saves the settings again: the full metadata, new
	// certificate included, is applied and the warning clears.
	r.resave(t)
	require.Equal(t, movedSSO, r.ssoPrefix(t), "the saved configuration is live")
	require.Empty(t, r.refreshProblem(t), "and the warning is gone")
}

func TestRefreshSAML_DoesNotApplyAReplacedCertificate(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md := r.replaced(t)
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "the running configuration is kept")
	r.requireNewCertWarning(t, md.IDPSSODescriptors[0].KeyDescriptors[0])

	r.resave(t)
	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}

// The warning follows the metadata: if the added certificate goes away again, a
// refresh applies and clears it.
func TestRefreshSAML_ThePendingWarningClearsWhenTheCertificateIsWithdrawn(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md, extra := r.withExtraCert(t, false)
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())
	r.requireNewCertWarning(t, extra)

	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}

// Removing trust is applied automatically, so a retired or revoked certificate
// stops being trusted without anyone having to notice.
func TestRefreshSAML_AppliesARefreshThatOnlyRemovesCertificates(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	// Trust two certificates, by an administrator's save.
	md, extra := r.withExtraCert(t, false)
	r.publish(md)
	r.resave(t)
	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t))

	// The IdP withdraws the second one (and moves its endpoint, to see the apply).
	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, movedSSO, r.ssoPrefix(t), "a subset is applied")
	require.Empty(t, r.refreshProblem(t))

	// The withdrawn certificate is no longer trusted: publishing it again is an
	// addition, and is not applied.
	again, _ := r.withExtraCert(t, true)
	again.IDPSSODescriptors[0].KeyDescriptors = r.moved().IDPSSODescriptors[0].KeyDescriptors
	again.IDPSSODescriptors[0].KeyDescriptors = append(again.IDPSSODescriptors[0].KeyDescriptors, extra)
	r.publish(again)
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, "new_certificate", r.refreshProblem(t))
}

func TestRefreshSAML_AppliesAnUnchangedCertificateSet(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}

// Metadata with no signing certificate would leave nobody able to sign in, and
// is what a stripped response looks like: not applied, and shown as invalid
// metadata rather than as a new certificate.
func TestRefreshSAML_DoesNotApplyMetadataWithNoSigningCertificate(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md := r.moved()
	md.IDPSSODescriptors[0].KeyDescriptors = nil
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t))
	require.Equal(t, "metadata_invalid", r.refreshProblem(t))
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

	md, _ := r.withExtraCert(t, true)
	r.publish(md)
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
// loaded whatever it holds, with no certificate or EntityID rule, because what the
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

// A certificate with no use attribute is a signing certificate to the library
// (it verifies with use "" and "signing"), so adding one is adding trust.
func TestRefreshSAML_DoesNotApplyACertificateWithNoUseAttribute(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	md, extra := r.withExtraCert(t, true)
	kds := md.IDPSSODescriptors[0].KeyDescriptors
	kds[len(kds)-1].Use = "" // no use: signing and encryption, and so signing to the library
	r.publish(md)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t))
	extra.Use = ""
	r.requireNewCertWarning(t, extra)
}

// The library fails every sign-in when any signing certificate in the metadata
// does not parse, so metadata with one good certificate and one unreadable one
// must not replace a working configuration, on the unattended refresh or on a
// manual save. Pasted XML is refused for the same reason.
func (r *refreshRig) publishWithAnUnreadableCert(t *testing.T) {
	t.Helper()
	md, _ := r.withExtraCert(t, true)
	kds := md.IDPSSODescriptors[0].KeyDescriptors
	kds[len(kds)-1].KeyInfo.X509Data.X509Certificates[0].Data = "bm90IGEgY2VydA=="
	r.publish(md)
}

func TestRefreshSAML_DoesNotApplyMetadataWithAnUnreadableCertificate(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publishWithAnUnreadableCert(t)
	r.h.srv.RefreshSAML(context.Background())

	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "the running configuration is kept")
	require.Equal(t, "metadata_invalid", r.refreshProblem(t))

	// The next good refresh clears it.
	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, movedSSO, r.ssoPrefix(t))
	require.Empty(t, r.refreshProblem(t))
}

func TestSaveSAMLConfig_AURLWithAnUnreadableCertificateKeepsTheLiveConfigAndWarns(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()

	r.publishWithAnUnreadableCert(t)
	res, body := r.sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": r.tls.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "could not be loaded")
	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "the running configuration is kept")
}

// failingSettings fails every read, as a database that is briefly unreachable
// would.
type failingSettings struct{ admin.Store }

func (failingSettings) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("injected settings read failure")
}

func (failingSettings) List(context.Context) (map[string][]byte, error) {
	return nil, errors.New("injected settings read failure")
}

// A settings read that fails during the hourly tick must not read as "SAML is
// not configured" and unload the middleware.
func TestRefreshSAML_AFailedSettingsReadKeepsTheLiveConfiguration(t *testing.T) {
	r, cleanup := newRefreshRig(t)
	defer cleanup()
	good := r.h.adminSvc

	r.h.srv.SetAdminServiceForTest(admin.NewService(failingSettings{adminstore.New(r.h.q)}))
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "still loaded")

	// And the same when the reload is the settings PATCH's or startup's.
	require.Error(t, r.h.srv.ReloadSAMLForTest(context.Background()))
	require.Equal(t, r.idp.idp.SSOURL.String(), r.ssoPrefix(t), "still loaded")

	// Once reads work again the next tick carries on as before.
	r.h.srv.SetAdminServiceForTest(good)
	r.publish(r.moved())
	r.h.srv.RefreshSAML(context.Background())
	require.Equal(t, movedSSO, r.ssoPrefix(t))
}
