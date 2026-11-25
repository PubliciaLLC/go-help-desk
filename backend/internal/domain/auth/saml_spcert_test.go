package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

func parseCert(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("not a CERTIFICATE PEM block: %q", certPEM)
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The pair is what the SAML middleware is built from, so the tests parse it the
// way NewSAMLMiddleware does and read the certificate back the way an IdP does.
func TestGenerateSPCertificate_Certificate(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	certPEM, _, err := auth.GenerateSPCertificate("help.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	c := parseCert(t, certPEM)

	checks := []struct {
		name string
		ok   bool
	}{
		{"CN is the host", c.Subject.CommonName == "help.example.com"},
		{"organisation is Go Help Desk", len(c.Subject.Organization) == 1 && c.Subject.Organization[0] == "Go Help Desk"},
		{"self-signed", c.Issuer.String() == c.Subject.String() && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil},
		{"SHA-256 with RSA", c.SignatureAlgorithm == x509.SHA256WithRSA},
		{"digitalSignature and keyEncipherment only", c.KeyUsage == x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment},
		{"not a CA", !c.IsCA},
		{"valid for ten years", c.NotAfter.Equal(now.AddDate(10, 0, 0))},
		{"valid from before now, for clock skew", c.NotBefore.Before(now) && now.Sub(c.NotBefore) <= 2*time.Hour},
		{"positive serial of at most 128 bits", c.SerialNumber.Sign() > 0 && c.SerialNumber.BitLen() <= 128},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Fatal("check failed")
			}
		})
	}
	pub, ok := c.PublicKey.(*rsa.PublicKey)
	if !ok || pub.N.BitLen() != 2048 {
		t.Fatalf("want an RSA 2048 key, got %T", c.PublicKey)
	}
}

func TestGenerateSPCertificate_KeyMatchesCertificateAndLoadsAsSAMLPair(t *testing.T) {
	certPEM, keyPEM, err := auth.GenerateSPCertificate("help.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// What PUT /admin/saml and NewSAMLMiddleware both do first.
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatalf("certificate and key do not form a pair: %v", err)
	}
	if _, ok := pair.PrivateKey.(*rsa.PrivateKey); !ok {
		t.Fatalf("want an RSA private key, got %T", pair.PrivateKey)
	}
	if !strings.Contains(keyPEM, "PRIVATE KEY") || strings.Contains(certPEM, "PRIVATE KEY") {
		t.Fatal("the key belongs in the key PEM and nowhere else")
	}
}

func TestGenerateSPCertificate_SubjectHost(t *testing.T) {
	cases := []struct{ name, host, wantCN string }{
		{"a host", "help.example.com", "help.example.com"},
		{"empty falls back", "", "go-help-desk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			certPEM, _, err := auth.GenerateSPCertificate(tc.host, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got := parseCert(t, certPEM).Subject.CommonName; got != tc.wantCN {
				t.Fatalf("CN = %q, want %q", got, tc.wantCN)
			}
		})
	}
}

// Two generations never share a serial or a key.
func TestGenerateSPCertificate_SerialsAndKeysAreUnique(t *testing.T) {
	serials := map[string]bool{}
	keys := map[string]bool{}
	for i := 0; i < 8; i++ {
		certPEM, keyPEM, err := auth.GenerateSPCertificate("h", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		s := parseCert(t, certPEM).SerialNumber.String()
		if serials[s] || keys[keyPEM] {
			t.Fatalf("generation %d repeats an earlier serial or key", i)
		}
		serials[s], keys[keyPEM] = true, true
	}
}

// selfSignedPEM is a self-signed certificate such as an administrator would
// make with openssl, with the given organisation.
func selfSignedPEM(t *testing.T, org string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "sp", Organization: []string{org}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestSPCertificateInfo(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	generated, _, err := auth.GenerateSPCertificate("h", now)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name          string
		pem           string
		wantOK        bool
		wantGenerated bool
	}{
		{"a generated certificate", generated, true, true},
		// Self-signed too, but not ours.
		{"an uploaded certificate", selfSignedPEM(t, "Example Corp"), true, false},
		// What openssl makes with this organisation (the repository's own test
		// fixture is one): self-signed, but a CA and with no key usage.
		{"an uploaded certificate with our organisation", selfSignedPEM(t, "Go Help Desk"), true, false},
		{"nothing stored", "", false, false},
		{"not a certificate", "not pem", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, ok := auth.SPCertificateInfo(tc.pem)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if info.Generated != tc.wantGenerated {
				t.Fatalf("Generated = %v, want %v", info.Generated, tc.wantGenerated)
			}
			if tc.wantGenerated && !info.NotAfter.Equal(now.AddDate(10, 0, 0)) {
				t.Fatalf("NotAfter = %v", info.NotAfter)
			}
			if len(info.SHA256) != 32*3-1 || strings.ToUpper(info.SHA256) != info.SHA256 {
				t.Fatalf("SHA256 %q is not colon-separated upper-case hex", info.SHA256)
			}
		})
	}
}
