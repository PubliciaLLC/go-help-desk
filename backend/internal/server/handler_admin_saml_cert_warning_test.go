package server_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/require"
)

// idpMetadataExpiringAt is the fake IdP's metadata with its signing certificate
// swapped for one that expires at notAfter.
func idpMetadataExpiringAt(t *testing.T, idp *fakeIdP, notAfter time.Time) *saml.EntityDescriptor {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "idp"},
		NotBefore: notAfter.Add(-24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	md := idp.idp.Metadata()
	for i := range md.IDPSSODescriptors {
		for j := range md.IDPSSODescriptors[i].KeyDescriptors {
			certs := md.IDPSSODescriptors[i].KeyDescriptors[j].KeyInfo.X509Data.X509Certificates
			for k := range certs {
				certs[k].Data = base64.StdEncoding.EncodeToString(der)
			}
		}
	}
	return md
}

// An expired IdP signing certificate fails every sign-in, and pasted metadata
// never refreshes itself, so the administrator is told in the banner before it
// happens, and after (#619).
func TestSecurityWarnings_SAMLIdPCertificateExpiry(t *testing.T) {
	tests := []struct {
		name        string
		notAfter    time.Time
		wantWarning bool
		wantExpired bool
	}{
		{"expired", time.Now().Add(-48 * time.Hour), true, true},
		{"expires within 30 days", time.Now().Add(29 * 24 * time.Hour), true, false},
		{"expires in more than 30 days", time.Now().Add(31 * 24 * time.Hour), false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
			configurePastedIdP(t, h, idpMetadataExpiringAt(t, idp, tc.notAfter))

			got := adminGet(t, sess, "/api/v1/admin/security-warnings")
			w, present := got["saml_idp_certificate"].(map[string]any)
			require.Equal(t, tc.wantWarning, present, "warnings: %v", got)
			if !tc.wantWarning {
				return
			}
			require.Equal(t, tc.wantExpired, w["expired"])
			expires, err := time.Parse(time.RFC3339, w["expires_at"].(string))
			require.NoError(t, err)
			require.WithinDuration(t, tc.notAfter, expires, 2*time.Second)
		})
	}
}

func TestSecurityWarnings_NoSAMLCertificateWarningWithoutSAML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res, body := adminSession(t, h).send(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.NotContains(t, string(body), "saml_idp_certificate")
}
