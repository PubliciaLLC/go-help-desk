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
