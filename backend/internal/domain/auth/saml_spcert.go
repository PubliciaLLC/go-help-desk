package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	// spCertOrganization is the subject organisation of a generated certificate,
	// and how SPCertificateInfo tells one from an administrator's own.
	spCertOrganization = "Go Help Desk"
	spCertFallbackCN   = "go-help-desk"
	spCertYears        = 10
	// spCertSkew is how far before the moment of generation the certificate is
	// valid from, so a server whose clock runs a little behind does not see a
	// certificate that is not yet valid.
	spCertSkew = time.Hour
)

// GenerateSPCertificate makes the service-provider certificate and private key
// SAML needs: a self-signed RSA 2048 certificate, SHA-256, valid for ten years
// from now, with subject CN host (spCertFallbackCN when host is empty) and
// organisation "Go Help Desk", a random 128-bit serial, and key usage
// digitalSignature and keyEncipherment. RSA, because an IdP that encrypts
// assertions (Microsoft Entra ID) encrypts to the certificate's RSA key.
// The key is PKCS#8 PEM; both are in the form tls.X509KeyPair reads.
func GenerateSPCertificate(host string, now time.Time) (certPEM, keyPEM string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generating the SP key: %w", err)
	}
	// [1, 2^128): the serial must be positive.
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, max.Sub(max, big.NewInt(1)))
	if err != nil {
		return "", "", fmt.Errorf("generating the SP certificate serial: %w", err)
	}
	serial.Add(serial, big.NewInt(1))

	if host == "" {
		host = spCertFallbackCN
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{spCertOrganization}},
		NotBefore:             now.Add(-spCertSkew),
		NotAfter:              now.AddDate(spCertYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("creating the SP certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("encoding the SP key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}

// SPCert is what is read off a stored service-provider certificate.
type SPCert struct {
	NotAfter time.Time
	// SHA256 is the DER certificate's fingerprint, as Fingerprint writes it.
	SHA256 string
	// Generated is true for a certificate GenerateSPCertificate made: self-signed,
	// organisation "Go Help Desk", not a CA, key usage exactly digitalSignature and
	// keyEncipherment. Read off the certificate rather
	// than stored beside it, so it cannot disagree with the pair however the
	// pair was written; an administrator's certificate made to look the same
	// reads as generated, which only changes a label.
	Generated bool
}

// madeByGenerateSPCertificate: self-signed, our organisation, and the rest of
// what GenerateSPCertificate sets that openssl does not by default (not a CA,
// exactly digitalSignature and keyEncipherment).
func madeByGenerateSPCertificate(c *x509.Certificate) bool {
	return len(c.Subject.Organization) == 1 && c.Subject.Organization[0] == spCertOrganization &&
		c.BasicConstraintsValid && !c.IsCA &&
		c.KeyUsage == x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment &&
		c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// SPCertificateInfo parses certPEM. ok is false when it holds no certificate
// that parses, which includes "" (nothing stored).
func SPCertificateInfo(certPEM string) (SPCert, bool) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return SPCert{}, false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return SPCert{}, false
	}
	sum := sha256.Sum256(c.Raw)
	return SPCert{
		NotAfter:  c.NotAfter,
		SHA256:    Fingerprint(sum[:]),
		Generated: madeByGenerateSPCertificate(c),
	}, true
}
