// Package auth contains SAML provider configuration and session helpers.
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"

	"github.com/crewjam/saml/samlsp"
)

// SAMLConfig holds the parameters needed to initialise a SAML service provider.
type SAMLConfig struct {
	// BaseURL is the external root URL of this service, e.g. https://helpdesk.example.com
	BaseURL string
	// MetadataURL is the URL of the IdP's SAML 2.0 metadata XML.
	MetadataURL string
	// CertPEM and KeyPEM are the PEM-encoded SP signing certificate and private key.
	CertPEM []byte
	KeyPEM  []byte
}

// NewSAMLMiddleware constructs a crewjam/saml middleware for the given config.
// CertPEM and KeyPEM are the raw PEM bytes — no files on disk are required.
//
// The SP metadata will be served at {BaseURL}/api/v1/auth/saml/metadata and
// the assertion consumer service at {BaseURL}/api/v1/auth/saml/acs.
func NewSAMLMiddleware(cfg SAMLConfig) (*samlsp.Middleware, error) {
	keyPair, err := tls.X509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing SAML certificate/key pair: %w", err)
	}
	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing SAML leaf certificate: %w", err)
	}

	// The SP root URL is set to the API auth prefix so that the computed
	// ACS and metadata URLs match our registered routes:
	//   {baseURL}/api/v1/auth/saml/acs
	//   {baseURL}/api/v1/auth/saml/metadata
	spURL, err := url.Parse(cfg.BaseURL + "/api/v1/auth")
	if err != nil {
		return nil, fmt.Errorf("parsing SP base URL: %w", err)
	}

	metadataURL, err := url.Parse(cfg.MetadataURL)
	if err != nil {
		return nil, fmt.Errorf("parsing IdP metadata URL: %w", err)
	}
	idpMeta, err := samlsp.FetchMetadata(context.Background(), http.DefaultClient, *metadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetching IdP metadata from %s: %w", cfg.MetadataURL, err)
	}

	opts := samlsp.Options{
		URL:         *spURL,
		Key:         keyPair.PrivateKey.(*rsa.PrivateKey),
		Certificate: keyPair.Leaf,
		IDPMetadata: idpMeta,
	}
	return samlsp.New(opts)
}
