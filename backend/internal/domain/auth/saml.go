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
	// BaseURL is the external URL of this service, e.g. https://helpdesk.example.com
	BaseURL string
	// MetadataURL is the URL of the IdP's metadata XML.
	MetadataURL string
	// CertFile and KeyFile are the PEM-encoded SP signing certificate and key.
	CertFile string
	KeyFile  string
}

// NewSAMLMiddleware constructs a crewjam/saml middleware for the given config.
// The returned *samlsp.Middleware handles /saml/login and /saml/acs routes
// and must be mounted at the service root.
func NewSAMLMiddleware(cfg SAMLConfig) (*samlsp.Middleware, error) {
	keyPair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading SAML key pair: %w", err)
	}
	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parsing SAML certificate: %w", err)
	}

	rootURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}

	metadataURL, err := url.Parse(cfg.MetadataURL)
	if err != nil {
		return nil, fmt.Errorf("parsing metadata URL: %w", err)
	}
	idpMeta, err := samlsp.FetchMetadata(context.Background(), http.DefaultClient, *metadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetching IdP metadata from %s: %w", cfg.MetadataURL, err)
	}

	opts := samlsp.Options{
		URL:         *rootURL,
		Key:         keyPair.PrivateKey.(*rsa.PrivateKey),
		Certificate: keyPair.Leaf,
		IDPMetadata: idpMeta,
	}
	return samlsp.New(opts)
}
