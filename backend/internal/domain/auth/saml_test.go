package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/stretchr/testify/require"
)

// selfSignedSP returns a PEM cert/key pair usable as an SP signing keypair.
func selfSignedSP(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-sp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

// TestNewSAMLMiddleware_HonoursContext pins the fix for an unbounded metadata
// fetch.
//
// The call previously passed context.Background() and http.DefaultClient, which
// has no timeout. An IdP that accepts the connection and then stalls would hang
// the SAML reload forever — and reloadSAML runs from the admin settings save,
// so the request simply never returns.
//
// A cancelled context must abort the fetch promptly rather than being ignored.
func TestNewSAMLMiddleware_HonoursContext(t *testing.T) {
	// A metadata endpoint that accepts the connection and never answers.
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-stalled
	}))
	defer idp.Close()

	certPEM, keyPEM := selfSignedSP(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	done := make(chan error, 1)
	go func() {
		_, err := auth.NewSAMLMiddleware(ctx, auth.SAMLConfig{
			BaseURL:     "https://helpdesk.example.com",
			MetadataURL: idp.URL + "/metadata",
			CertPEM:     certPEM,
			KeyPEM:      keyPEM,
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err, "a cancelled context must abort the metadata fetch")
	case <-time.After(5 * time.Second):
		t.Fatal("metadata fetch ignored the cancelled context and hung")
	}
}

// samlIDPMetadataXML is TestShib's real IdP metadata fixture, vendored
// (unmodified) from github.com/crewjam/saml's own test suite
// (samlsp/testdata/idp_metadata.xml) — genuine, parseable SAML 2.0 IdP
// metadata, needed here to get past FetchMetadata and all the way to a real
// *samlsp.Middleware whose computed routes this test can inspect.
const samlIDPMetadataXML = `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" xmlns:ds="http://www.w3.org/2000/09/xmldsig#" entityID="https://idp.testshib.org/idp/shibboleth">
	<IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
		<KeyDescriptor>
			<ds:KeyInfo>
				<ds:X509Data>
					<ds:X509Certificate>MIIEDjCCAvagAwIBAgIBADANBgkqhkiG9w0BAQUFADBnMQswCQYDVQQGEwJVUzEV
                            MBMGA1UECBMMUGVubnN5bHZhbmlhMRMwEQYDVQQHEwpQaXR0c2J1cmdoMREwDwYD
                            VQQKEwhUZXN0U2hpYjEZMBcGA1UEAxMQaWRwLnRlc3RzaGliLm9yZzAeFw0wNjA4
                            MzAyMTEyMjVaFw0xNjA4MjcyMTEyMjVaMGcxCzAJBgNVBAYTAlVTMRUwEwYDVQQI
                            EwxQZW5uc3lsdmFuaWExEzARBgNVBAcTClBpdHRzYnVyZ2gxETAPBgNVBAoTCFRl
                            c3RTaGliMRkwFwYDVQQDExBpZHAudGVzdHNoaWIub3JnMIIBIjANBgkqhkiG9w0B
                            AQEFAAOCAQ8AMIIBCgKCAQEArYkCGuTmJp9eAOSGHwRJo1SNatB5ZOKqDM9ysg7C
                            yVTDClcpu93gSP10nH4gkCZOlnESNgttg0r+MqL8tfJC6ybddEFB3YBo8PZajKSe
                            3OQ01Ow3yT4I+Wdg1tsTpSge9gEz7SrC07EkYmHuPtd71CHiUaCWDv+xVfUQX0aT
                            NPFmDixzUjoYzbGDrtAyCqA8f9CN2txIfJnpHE6q6CmKcoLADS4UrNPlhHSzd614
                            kR/JYiks0K4kbRqCQF0Dv0P5Di+rEfefC6glV8ysC8dB5/9nb0yh/ojRuJGmgMWH
                            gWk6h0ihjihqiu4jACovUZ7vVOCgSE5Ipn7OIwqd93zp2wIDAQABo4HEMIHBMB0G
                            A1UdDgQWBBSsBQ869nh83KqZr5jArr4/7b+QazCBkQYDVR0jBIGJMIGGgBSsBQ86
                            9nh83KqZr5jArr4/7b+Qa6FrpGkwZzELMAkGA1UEBhMCVVMxFTATBgNVBAgTDFBl
                            bm5zeWx2YW5pYTETMBEGA1UEBxMKUGl0dHNidXJnaDERMA8GA1UEChMIVGVzdFNo
                            aWIxGTAXBgNVBAMTEGlkcC50ZXN0c2hpYi5vcmeCAQAwDAYDVR0TBAUwAwEB/zAN
                            BgkqhkiG9w0BAQUFAAOCAQEAjR29PhrCbk8qLN5MFfSVk98t3CT9jHZoYxd8QMRL
                            I4j7iYQxXiGJTT1FXs1nd4Rha9un+LqTfeMMYqISdDDI6tv8iNpkOAvZZUosVkUo
                            93pv1T0RPz35hcHHYq2yee59HJOco2bFlcsH8JBXRSRrJ3Q7Eut+z9uo80JdGNJ4
                            /SJy5UorZ8KazGj16lfJhOBXldgrhppQBb0Nq6HKHguqmwRfJ+WkxemZXzhediAj
                            Geka8nz8JjwxpUjAiSWYKLtJhGEaTqCYxCCX2Dw+dOTqUzHOZ7WKv4JXPK5G/Uhr
                            8K/qhmFT2nIQi538n6rVYLeWj8Bbnl+ev0peYzxFyF5sQA==</ds:X509Certificate>
				</ds:X509Data>
			</ds:KeyInfo>
		</KeyDescriptor>
		<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.testshib.org/idp/profile/SAML2/Redirect/SSO" />
	</IDPSSODescriptor>
</EntityDescriptor>`

// TestNewSAMLMiddleware_ComputesRoutesMatchingTheServerMounts pins a
// pre-existing bug found while testing #304, unrelated to anything that PR
// itself changed: the SP root URL passed to samlsp had no trailing slash,
// so url.URL.ResolveReference's relative-path resolution (RFC 3986 §5.3,
// which replaces everything after the LAST slash in the base path, not
// everything after the base path itself) dropped the "auth" segment —
// producing {baseURL}/api/v1/saml/metadata and .../saml/acs instead of the
// .../api/v1/auth/saml/metadata and .../auth/saml/acs routes
// internal/server/routes.go actually registers. samlsp.Middleware.ServeHTTP
// compares the request path against these computed URLs with ==, so every
// real request to the registered routes missed and fell through to a 404 —
// SAML could never complete a login or serve metadata to an IdP, in any
// configuration. See NewSAMLMiddleware's own comment on the exact mechanism.
func TestNewSAMLMiddleware_ComputesRoutesMatchingTheServerMounts(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(samlIDPMetadataXML))
	}))
	defer idp.Close()

	certPEM, keyPEM := selfSignedSP(t)
	mw, err := auth.NewSAMLMiddleware(context.Background(), auth.SAMLConfig{
		BaseURL:     "https://helpdesk.example.com",
		MetadataURL: idp.URL,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	})
	require.NoError(t, err)

	require.Equal(t, "/api/v1/auth/saml/metadata", mw.ServiceProvider.MetadataURL.Path,
		"must match the GET /saml/metadata route mounted under /api/v1/auth in routes.go")
	require.Equal(t, "/api/v1/auth/saml/acs", mw.ServiceProvider.AcsURL.Path,
		"must match the POST /saml/acs route mounted under /api/v1/auth in routes.go")
}

// TestNewSAMLMiddleware_RejectsBadKeyPair guards the error path that runs
// before any network call, so a misconfigured keypair fails fast and clearly.
func TestNewSAMLMiddleware_RejectsBadKeyPair(t *testing.T) {
	_, err := auth.NewSAMLMiddleware(context.Background(), auth.SAMLConfig{
		BaseURL:     "https://helpdesk.example.com",
		MetadataURL: "https://idp.example.com/metadata",
		CertPEM:     []byte("not a certificate"),
		KeyPEM:      []byte("not a key"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SAML certificate/key pair",
		"the error must say which part of the config is wrong")
}

// TestNewSAMLMiddleware_HandoverCookieLivesFiveMinutes pins that the SAML
// library's hand-over cookie and its JWT are configured for exactly 5 minutes
// (#337), not the library's default of an hour. The lifetime matters only for
// a copy that never reached /complete; five minutes is enough because the
// redirect takes milliseconds, and five rather than seconds because the JWT
// is checked against whichever replica serves /complete.
func TestNewSAMLMiddleware_HandoverCookieLivesFiveMinutes(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(samlIDPMetadataXML))
	}))
	defer idp.Close()

	certPEM, keyPEM := selfSignedSP(t)
	mw, err := auth.NewSAMLMiddleware(context.Background(), auth.SAMLConfig{
		BaseURL:     "https://helpdesk.example.com",
		MetadataURL: idp.URL,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	})
	require.NoError(t, err)

	// Create a session to capture the cookie and JWT.
	rec := httptest.NewRecorder()
	mw.Session.CreateSession(rec, httptest.NewRequest("POST", "/", nil), &saml.Assertion{
		Subject: &saml.Subject{
			NameID: &saml.NameID{Value: "x"},
		},
	})

	// Assert the cookie's MaxAge is exactly 300 seconds (5 minutes).
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "exactly one cookie must be created")
	require.Equal(t, 300, cookies[0].MaxAge, "cookie's MaxAge must be 300 seconds (5 minutes)")

	// Assert the provider is the value type that the handler expects.
	cp, ok := mw.Session.(samlsp.CookieSessionProvider)
	require.True(t, ok, "Session must be a CookieSessionProvider value type")

	// Assert the JWT's lifetime is also 300 seconds.
	cookie := cookies[0]
	sess, err := cp.Codec.Decode(cookie.Value)
	require.NoError(t, err)
	claims, ok := sess.(samlsp.JWTSessionClaims)
	require.True(t, ok, "session must be decodable as JWTSessionClaims")
	require.Equal(t, int64(300), claims.ExpiresAt-claims.IssuedAt,
		"JWT exp and iat must differ by exactly 300 seconds")
}
