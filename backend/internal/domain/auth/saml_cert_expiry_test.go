package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"testing"
	"time"

	"github.com/crewjam/saml"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// certDescriptor is a KeyDescriptor holding a fresh certificate that expires
// at notAfter. use is the KeyDescriptor's use attribute.
func certDescriptor(t *testing.T, use string, notAfter time.Time) saml.KeyDescriptor {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "idp"},
		NotBefore: notAfter.Add(-24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var kd saml.KeyDescriptor
	kd.Use = use
	kd.KeyInfo.X509Data.X509Certificates = []saml.X509Certificate{{Data: base64.StdEncoding.EncodeToString(der)}}
	return kd
}

func TestIdPSigningCertExpiry(t *testing.T) {
	soon := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	later := time.Now().Add(400 * 24 * time.Hour).Truncate(time.Second)
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)

	tests := []struct {
		name string
		kds  []saml.KeyDescriptor
		want time.Time // zero: none found
	}{
		{"one certificate", []saml.KeyDescriptor{certDescriptor(t, "", soon)}, soon},
		{"use=signing", []saml.KeyDescriptor{certDescriptor(t, "signing", past)}, past},
		// During a rollover the IdP publishes both. Either can sign, so the
		// expiring one alone is no reason to warn: the newest decides.
		{"a rollover pair: the later one decides", []saml.KeyDescriptor{certDescriptor(t, "", soon), certDescriptor(t, "", later)}, later},
		{"encryption keys are not signing keys", []saml.KeyDescriptor{certDescriptor(t, "encryption", later), certDescriptor(t, "signing", soon)}, soon},
		{"only an encryption key", []saml.KeyDescriptor{certDescriptor(t, "encryption", later)}, time.Time{}},
		{"none", nil, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ed := &saml.EntityDescriptor{IDPSSODescriptors: []saml.IDPSSODescriptor{
				{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{KeyDescriptors: tc.kds}}},
			}}
			got, ok := auth.IdPSigningCertExpiry(ed)
			if tc.want.IsZero() {
				if ok {
					t.Fatalf("got %v, want none", got)
				}
				return
			}
			if !ok || !got.Equal(tc.want) {
				t.Fatalf("got %v (ok=%v), want %v", got, ok, tc.want)
			}
		})
	}

	t.Run("nil metadata (expiry)", func(t *testing.T) {
		if _, ok := auth.IdPSigningCertExpiry(nil); ok {
			t.Fatal("got a date from nothing")
		}
	})
	t.Run("a certificate that does not parse is skipped", func(t *testing.T) {
		bad := certDescriptor(t, "", soon)
		bad.KeyInfo.X509Data.X509Certificates[0].Data = "bm90IGEgY2VydA=="
		ed := &saml.EntityDescriptor{IDPSSODescriptors: []saml.IDPSSODescriptor{
			{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{KeyDescriptors: []saml.KeyDescriptor{bad, certDescriptor(t, "", later)}}}},
		}}
		got, ok := auth.IdPSigningCertExpiry(ed)
		if !ok || !got.Equal(later) {
			t.Fatalf("got %v (ok=%v), want %v", got, ok, later)
		}
	})
}

// A refresh may only remove trust or leave it as it is. Adding a certificate is
// what an attacker who controls the response would do, next to a real one or
// instead of it, so any addition needs an administrator.
func TestAddedSigningCerts(t *testing.T) {
	a := certDescriptor(t, "", time.Now().Add(100*24*time.Hour))
	b := certDescriptor(t, "", time.Now().Add(200*24*time.Hour))
	c := certDescriptor(t, "", time.Now().Add(300*24*time.Hour))
	enc := a
	enc.Use = "encryption"
	md := func(kds ...saml.KeyDescriptor) *saml.EntityDescriptor {
		return &saml.EntityDescriptor{IDPSSODescriptors: []saml.IDPSSODescriptor{
			{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{KeyDescriptors: kds}}},
		}}
	}
	tests := []struct {
		name     string
		old, new *saml.EntityDescriptor
		want     []saml.KeyDescriptor // the descriptors whose certificates are new
	}{
		{"identical", md(a), md(a), nil},
		{"one removed", md(a, b), md(a), nil},
		{"all but one removed", md(a, b, c), md(b), nil},
		{"one added beside the old one", md(a), md(a, b), []saml.KeyDescriptor{b}},
		{"replaced", md(a), md(b), []saml.KeyDescriptor{b}},
		{"one kept, one replaced", md(a, b), md(b, c), []saml.KeyDescriptor{c}},
		{"two added", md(a), md(a, b, c), []saml.KeyDescriptor{b, c}},
		{"the same certificate as an encryption key is not a signing cert", md(a), md(enc), nil},
		{"a new certificate as an encryption key is not a signing cert", md(a), md(a, certDescriptor(t, "encryption", time.Now().Add(time.Hour))), nil},
		{"nothing before: everything is new", md(), md(a), []saml.KeyDescriptor{a}},
		{"nil old: everything is new", nil, md(a), []saml.KeyDescriptor{a}},
		{"nil new", md(a), nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := auth.AddedSigningCerts(tc.old, tc.new)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d added, want %d: %v", len(got), len(tc.want), got)
			}
			for i, kd := range tc.want {
				der, err := base64.StdEncoding.DecodeString(kd.KeyInfo.X509Data.X509Certificates[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(der)
				if want := auth.Fingerprint(sum[:]); got[i].SHA256 != want {
					t.Errorf("added[%d].SHA256 = %s, want %s", i, got[i].SHA256, want)
				}
				cert, _ := x509.ParseCertificate(der)
				if !got[i].NotAfter.Equal(cert.NotAfter) {
					t.Errorf("added[%d].NotAfter = %v, want %v", i, got[i].NotAfter, cert.NotAfter)
				}
			}
		})
	}
}
