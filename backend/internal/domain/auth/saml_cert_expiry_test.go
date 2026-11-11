package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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

// A refresh may swap the IdP's trust anchor only when the published
// certificates overlap: the normal rollover publishes old and new together.
func TestSigningCertsOverlap(t *testing.T) {
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
		want     bool
	}{
		{"identical", md(a), md(a), true},
		{"rollover: old published alongside new", md(a), md(a, b), true},
		{"rollover: one of two old certs remains", md(a, b), md(b, c), true},
		{"full replacement", md(a), md(b), false},
		{"full replacement of a pair", md(a, b), md(c), false},
		{"the old cert only as an encryption key does not count", md(a), md(enc, b), false},
		{"old has no signing cert", md(), md(a), false},
		{"new has no signing cert", md(a), md(), false},
		{"nil old", nil, md(a), false},
		{"nil new", md(a), nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.SigningCertsOverlap(tc.old, tc.new); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
