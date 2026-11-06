package auth

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

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
	ed, err := samlsp.ParseMetadata(data)
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
	n := 0
	for _, d := range ed.IDPSSODescriptors {
		for _, kd := range d.KeyDescriptors {
			if kd.Use != "" && kd.Use != "signing" {
				continue
			}
			for _, c := range kd.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(certWhitespace.ReplaceAllString(c.Data, ""))
				if err != nil {
					return fmt.Errorf("its signing certificate is not base64: %v", err)
				}
				if _, err := x509.ParseCertificate(der); err != nil {
					return fmt.Errorf("its signing certificate is not an X.509 certificate: %v", err)
				}
				n++
			}
		}
	}
	if n == 0 {
		return errors.New("it has no signing certificate (a KeyDescriptor with use=\"signing\" or no use), so no sign-in could be verified")
	}
	return nil
}

// certWhitespace is the whitespace crewjam strips before decoding (`\s+`).
var certWhitespace = regexp.MustCompile(`\s+`)
