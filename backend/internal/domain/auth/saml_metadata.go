package auth

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// MaxIdPMetadataXMLBytes caps pasted IdP metadata. A single IdP's metadata is
// a few KB (Google, Okta) to about 25 KB (Entra ID, with its WS-Fed role
// descriptors and signature); 256 KiB leaves ten times that, and stays well
// inside the 1 MiB JSON body cap (respond.go maxJSONBody) after JSON escaping
// and the certificate and key in the same request. Federation aggregates
// (EntitiesDescriptor with thousands of entities) are not a supported input.
const MaxIdPMetadataXMLBytes = 256 << 10

var (
	ErrIdPMetadataTooLarge = errors.New("IdP metadata XML is larger than 256 KiB")
	ErrIdPMetadataInvalid  = errors.New("not usable SAML IdP metadata")
)

// declaredEncodingRE matches the encoding in the XML declaration at the very
// start of a document (after an optional byte order mark and whitespace), and
// nothing later in it.
var declaredEncodingRE = regexp.MustCompile(`^(\x{FEFF}?\s*<\?xml\s[^>]*?\bencoding\s*=\s*)(?:"[^"]*"|'[^']*')`)

// ParseIdPMetadataXML parses pasted IdP metadata with the parser the URL fetch
// uses (samlsp.ParseMetadata: an xml-roundtrip-validator pass, then
// encoding/xml), so anything that loads from a URL loads pasted, and the
// reverse.
//
// Unlike the URL path it also insists on an IDPSSODescriptor: this instance's
// own SP metadata parses without error and yields none, and it is shown on the
// same settings page, so it is the likeliest paste mistake. Without the check
// that mistake saves cleanly and fails only at sign-in.
func ParseIdPMetadataXML(data []byte) (*saml.EntityDescriptor, error) {
	if len(data) > MaxIdPMetadataXMLBytes {
		return nil, ErrIdPMetadataTooLarge
	}
	// The text is already UTF-8 here (a JSON string), whatever the file it was
	// read from declared; a UTF-16 file keeps that declaration and encoding/xml
	// has no decoder for it (#619). The stored XML is not changed.
	ed, err := samlsp.ParseMetadata(declaredEncodingRE.ReplaceAll(data, []byte(`${1}"UTF-8"`)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdPMetadataInvalid, err)
	}
	if len(ed.IDPSSODescriptors) == 0 {
		return nil, fmt.Errorf("%w: it has no IDPSSODescriptor; this looks like a service provider's metadata, not the identity provider's", ErrIdPMetadataInvalid)
	}
	if err := checkIdPSigningCerts(ed); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdPMetadataInvalid, err)
	}
	return ed, nil
}

// checkIdPSigningCerts makes the same certificate selection the SP makes when
// it verifies an assertion (crewjam's ServiceProvider.getIDPSigningCerts: every
// KeyDescriptor whose use is "signing" or unset), and fails where that fails:
// no certificate, or one that is not base64 DER X.509. Metadata like that
// loads, so without this it would save cleanly, count as reachable for the
// lockout guard (#300), and then refuse every sign-in. Expiry is not checked:
// a certificate expires in place whatever its source.
func checkIdPSigningCerts(ed *saml.EntityDescriptor) error {
	n, err := readIdPSigningCerts(ed)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("it has no signing certificate (a KeyDescriptor with use=\"signing\" or no use), so no sign-in could be verified")
	}
	return nil
}

// readIdPSigningCerts counts the signing certificates and fails on one that is
// not base64 DER X.509, which is what the library fails on. Zero is not an
// error here; checkIdPSigningCerts adds that.
func readIdPSigningCerts(ed *saml.EntityDescriptor) (int, error) {
	n := 0
	for _, d := range ed.IDPSSODescriptors {
		for _, kd := range d.KeyDescriptors {
			if kd.Use != "" && kd.Use != "signing" {
				continue
			}
			for _, c := range kd.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(certWhitespace.ReplaceAllString(c.Data, ""))
				if err != nil {
					return 0, fmt.Errorf("its signing certificate is not base64: %v", err)
				}
				if _, err := x509.ParseCertificate(der); err != nil {
					return 0, fmt.Errorf("its signing certificate is not an X.509 certificate: %v", err)
				}
				n++
			}
		}
	}
	return n, nil
}

// certWhitespace is the whitespace crewjam strips before decoding (`\s+`).
var certWhitespace = regexp.MustCompile(`\s+`)

// idpSigningCerts are the certificates the SP would verify an assertion with
// (the same selection as checkIdPSigningCerts), skipping any that do not parse.
func idpSigningCerts(ed *saml.EntityDescriptor) []*x509.Certificate {
	if ed == nil {
		return nil
	}
	var certs []*x509.Certificate
	for _, d := range ed.IDPSSODescriptors {
		for _, kd := range d.KeyDescriptors {
			if kd.Use != "" && kd.Use != "signing" {
				continue
			}
			for _, c := range kd.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(certWhitespace.ReplaceAllString(c.Data, ""))
				if err != nil {
					continue
				}
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					continue
				}
				certs = append(certs, cert)
			}
		}
	}
	return certs
}

// CertSummary is what an administrator needs to compare a certificate with the
// one their identity provider shows.
type CertSummary struct {
	// SHA256 is the fingerprint of the DER certificate, as upper-case hex pairs
	// separated by colons (the form identity providers display).
	SHA256   string
	NotAfter time.Time
}

// Fingerprint formats a SHA-256 digest as AA:BB:CC.
func Fingerprint(sum []byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// AddedSigningCerts lists the signing certificates published in updated that old
// does not have, compared by the certificate itself. It is the test for applying
// refreshed metadata without an administrator: a refresh may remove trust (a
// retired or revoked certificate) or leave it alone, but may not add any. An
// attacker who controls the response adds a certificate they hold the key for,
// either beside a real one or instead of it, so adding is what needs a person.
func AddedSigningCerts(old, updated *saml.EntityDescriptor) []CertSummary {
	have := map[string]bool{}
	for _, c := range idpSigningCerts(old) {
		have[string(c.Raw)] = true
	}
	var added []CertSummary
	for _, c := range idpSigningCerts(updated) {
		if have[string(c.Raw)] {
			continue
		}
		have[string(c.Raw)] = true // listed twice in the metadata: reported once
		sum := sha256.Sum256(c.Raw)
		added = append(added, CertSummary{SHA256: Fingerprint(sum[:]), NotAfter: c.NotAfter})
	}
	return added
}

// HasSigningCerts reports whether ed has a signing certificate that parses.
func HasSigningCerts(ed *saml.EntityDescriptor) bool { return len(idpSigningCerts(ed)) > 0 }

// IdPSigningCertExpiry is when the IdP's signing certificate stops being good:
// the latest NotAfter among the certificates the SP would verify with (the same
// selection as checkIdPSigningCerts). The latest, not the earliest, because an
// IdP rolling its certificate publishes the old and the new together and may
// sign with either; only when the newest is going or gone has nothing safe been
// published. An expired certificate fails every sign-in (#619).
//
// False when the metadata has no certificate that parses. Lenient by design:
// this reads the live metadata to raise a warning, and a warning that cannot be
// computed is silent, not an error.
func IdPSigningCertExpiry(ed *saml.EntityDescriptor) (time.Time, bool) {
	var latest time.Time
	for _, cert := range idpSigningCerts(ed) {
		if cert.NotAfter.After(latest) {
			latest = cert.NotAfter
		}
	}
	return latest, !latest.IsZero()
}
