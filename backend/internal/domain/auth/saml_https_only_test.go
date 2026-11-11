package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// The periodic metadata refresh runs with nobody watching, so it may only trust
// what arrives over TLS: not an http URL, and not a redirect that leaves https.
func TestNewSAMLMiddleware_HTTPSOnly(t *testing.T) {
	certPEM, keyPEM := selfSignedSP(t)
	cfg := func(url string, client *http.Client, httpsOnly bool) auth.SAMLConfig {
		return auth.SAMLConfig{
			BaseURL: "https://hd.test", MetadataURL: url, CertPEM: certPEM, KeyPEM: keyPEM,
			HTTPClient: client, HTTPSOnly: httpsOnly,
		}
	}
	serve := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(samlIDPMetadataXML)) }

	t.Run("an https URL loads", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(serve))
		defer srv.Close()
		if _, err := auth.NewSAMLMiddleware(context.Background(), cfg(srv.URL, srv.Client(), true)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an http URL is refused without a request", func(t *testing.T) {
		var hits atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); serve(w, r) }))
		defer srv.Close()
		_, err := auth.NewSAMLMiddleware(context.Background(), cfg(srv.URL, nil, true))
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("err = %v, want a refusal naming https", err)
		}
		if hits.Load() != 0 {
			t.Fatal("the http URL was requested")
		}
	})

	t.Run("an http URL still loads when https is not required (a manual save)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(serve))
		defer srv.Close()
		if _, err := auth.NewSAMLMiddleware(context.Background(), cfg(srv.URL, nil, false)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a redirect from https to http is refused", func(t *testing.T) {
		var plainHits atomic.Int64
		plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { plainHits.Add(1); serve(w, r) }))
		defer plain.Close()
		tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, plain.URL, http.StatusFound)
		}))
		defer tls.Close()
		if _, err := auth.NewSAMLMiddleware(context.Background(), cfg(tls.URL, tls.Client(), true)); err == nil {
			t.Fatal("followed a redirect to http")
		}
		if plainHits.Load() != 0 {
			t.Fatal("the http redirect target was requested")
		}
	})
}

// Fetched metadata gets the signing-certificate check pasted XML gets at save,
// since the library fails every sign-in when a signing certificate is unreadable.
func TestNewSAMLMiddleware_FetchedMetadataWithAnUnreadableCertificateIsRefused(t *testing.T) {
	certPEM, keyPEM := selfSignedSP(t)
	bad := samlCertRE.ReplaceAllString(samlIDPMetadataXML, "${1}bm90IGEgY2VydA==${2}")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(bad)) }))
	defer srv.Close()

	_, err := auth.NewSAMLMiddleware(context.Background(), auth.SAMLConfig{
		BaseURL: "https://hd.test", MetadataURL: srv.URL, CertPEM: certPEM, KeyPEM: keyPEM,
	})
	if !errors.Is(err, auth.ErrIdPMetadataInvalid) {
		t.Fatalf("err = %v, want ErrIdPMetadataInvalid", err)
	}
}
