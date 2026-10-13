package auth

import (
	"errors"
	"fmt"

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
	return ed, nil
}
