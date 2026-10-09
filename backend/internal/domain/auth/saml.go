// Package auth contains SAML provider configuration and session helpers.
package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// metadataFetchTimeout bounds the IdP metadata fetch. It runs while SAML is
// being (re)configured, so an IdP that accepts the connection and then stalls
// would otherwise hang the reload indefinitely, with the admin UI simply never
// returning. The client is safehttp's, which carries this timeout and refuses
// internal addresses.
const metadataFetchTimeout = 15 * time.Second

// SAMLHandoverMaxAge is how long the SAML library's own "token" cookie, and
// the JWT inside it, stay valid. The library's default is an hour, but the
// cookie only carries the assertion from the ACS to /auth/saml/complete, one
// redirect later, where it is spent (#337). The lifetime matters only for a
// copy that never got there. Five minutes, not seconds, because the JWT's
// expiry is checked against the clock of whichever replica serves /complete.
const SAMLHandoverMaxAge = 5 * time.Minute

// HandoverCodec is the library's JWT session codec with one change: every
// token it mints carries a random jti.
//
// /auth/saml/complete records each hand-over cookie it accepts by the SHA-256
// of the JWT's header.payload, and refuses one it has seen before (#337).
// Without a jti, that payload is the assertion's attributes plus iat, nbf and
// exp at one-second precision, so one person signing in twice within the
// same second gets two tokens whose header.payload is byte-for-byte equal,
// and the second, genuine sign-in is refused as a replay. The jti makes each
// token's header.payload, and so its spend key, unique.
type HandoverCodec struct {
	samlsp.JWTSessionCodec
}

// New builds the session exactly as the embedded codec does, then sets a
// random jti on it (see HandoverCodec for why).
//
// The claims are returned by value: the embedded Encode and the /complete
// handler both type-assert to samlsp.JWTSessionClaims, not a pointer, and
// Encode panics on anything else.
func (c HandoverCodec) New(assertion *saml.Assertion) (samlsp.Session, error) {
	sess, err := c.JWTSessionCodec.New(assertion)
	if err != nil {
		return nil, err
	}

	// The embedded codec returns this type today. If an upgrade changes that,
	// fail the sign-in rather than mint a token without a jti, which would
	// bring back the same-second collision.
	claims, ok := sess.(samlsp.JWTSessionClaims)
	if !ok {
		return nil, fmt.Errorf("expected samlsp.JWTSessionClaims, got %T", sess)
	}

	// 128 random bits: a collision between two live tokens is not a case to
	// plan for.
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return nil, fmt.Errorf("generating jti: %w", err)
	}
	claims.Id = base64.RawURLEncoding.EncodeToString(jtiBytes)
	return claims, nil
}

// NewHandoverCodec constructs a HandoverCodec from SAML options, configured
// with the handover lifetime.
func NewHandoverCodec(opts samlsp.Options) HandoverCodec {
	codec := samlsp.DefaultSessionCodec(opts)
	codec.MaxAge = SAMLHandoverMaxAge
	return HandoverCodec{JWTSessionCodec: codec}
}

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
// ctx bounds the IdP metadata fetch; see metadataFetchTimeout.
//
// The SP metadata will be served at {BaseURL}/api/v1/auth/saml/metadata and
// the assertion consumer service at {BaseURL}/api/v1/auth/saml/acs.
func NewSAMLMiddleware(ctx context.Context, cfg SAMLConfig) (*samlsp.Middleware, error) {
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
	//
	// The trailing slash is load-bearing, not cosmetic: samlsp.DefaultServiceProvider
	// builds those two URLs via url.ResolveReference(&url.URL{Path: "saml/metadata"})
	// (and "saml/acs"), which is RFC 3986 §5.3 relative resolution — it replaces
	// everything after the LAST slash in the base path, not everything after the
	// base path itself. Without the trailing slash, "auth" is what gets replaced,
	// producing {baseURL}/api/v1/saml/metadata: one path segment short of the
	// route this server actually registers. samlsp.Middleware.ServeHTTP compares
	// r.URL.Path against that computed value with ==, so every real request to
	// the registered route missed it and fell through to a 404 — SAML could
	// never complete a login or serve its own metadata to an IdP, regardless of
	// how correctly everything else here is configured. Caught by a test for an
	// unrelated #304 regression (there was previously no test exercising the SAML
	// metadata route through the live server at all) rather than anything about
	// this function's own logic, which is why it went unnoticed until now.
	spURL, err := url.Parse(cfg.BaseURL + "/api/v1/auth/")
	if err != nil {
		return nil, fmt.Errorf("parsing SP base URL: %w", err)
	}

	metadataURL, err := url.Parse(cfg.MetadataURL)
	if err != nil {
		return nil, fmt.Errorf("parsing IdP metadata URL: %w", err)
	}
	// The caller's context is honoured rather than discarded, so a shutdown or
	// a cancelled admin request stops the fetch instead of outliving it.
	fetchCtx, cancel := context.WithTimeout(ctx, metadataFetchTimeout)
	defer cancel()

	// NOT address-guarded, deliberately: a self-hosted deployment commonly runs
	// its IdP on the same private network, so refusing private addresses here
	// would break a normal topology on upgrade. The exposure is bounded instead
	// by the timeout and by not echoing the fetch error back to the caller —
	// see handleSaveSAMLConfig. Webhook targets ARE guarded, because those are
	// external by definition.
	idpMeta, err := samlsp.FetchMetadata(fetchCtx, &http.Client{Timeout: metadataFetchTimeout}, *metadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetching IdP metadata from %s: %w", cfg.MetadataURL, err)
	}

	// samlsp.Options wants a crypto.Signer, not specifically an RSA key —
	// tls.X509KeyPair above happily parses and validates an ECDSA or Ed25519
	// pair too, so asserting straight to *rsa.PrivateKey turned a
	// well-formed non-RSA keypair into an unrecovered panic (a 500 with no
	// useful message) instead of the plain configuration error this is.
	signer, ok := keyPair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("SAML private key does not support signing (got %T)", keyPair.PrivateKey)
	}
	opts := samlsp.Options{
		URL:         *spURL,
		Key:         signer,
		Certificate: keyPair.Leaf,
		IDPMetadata: idpMeta,
	}
	mw, err := samlsp.New(opts)
	if err != nil {
		return nil, err
	}
	// Both lifetimes: the cookie's Max-Age, and the JWT's exp, which is what
	// is actually enforced (a browser can keep a cookie past its Max-Age).
	// Use HandoverCodec to ensure each minted JWT gets a random jti; without
	// it, two mints for one person in the same second share the same
	// header.payload, and the spend (keyed on SHA-256 of header.payload)
	// wrongly rejects the second sign-in (#337).
	codec := NewHandoverCodec(opts)
	session := samlsp.DefaultSessionProvider(opts)
	session.MaxAge = SAMLHandoverMaxAge
	session.Codec = codec
	mw.Session = session
	return mw, nil
}
