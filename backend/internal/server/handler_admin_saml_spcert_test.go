package server_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The service-provider certificate and key are generated, not uploaded: the
// first PUT /admin/saml that has IdP metadata and no stored pair makes one. See
// DESIGN.md, "The service-provider certificate and key are generated".

const (
	samlPath    = "/api/v1/admin/saml"
	pairInvalid = "certificate and private key do not match or are invalid"
)

// samlCertEntity is the audit entity id of the SP certificate entries
// (settingEntityID for saml_cert_pem).
var samlCertEntity = uuid.NewSHA1(uuid.NameSpaceOID, []byte("setting:saml_cert_pem"))

func putSAML(t *testing.T, sess *session, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	return sess.send(t, http.MethodPut, samlPath, body)
}

// firstSave is the save an administrator makes on a fresh install: the IdP's
// metadata, nothing else.
func firstSave(t *testing.T, h *harness, sess *session) {
	t.Helper()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	res, body := putSAML(t, sess, map[string]any{"metadata_url": idp.srv.URL})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}

func storedPair(t *testing.T, h *harness) (cert, key string) {
	t.Helper()
	_, cert, key = h.adminSvc.GetSAMLConfig(context.Background())
	return cert, key
}

func parsePEMCert(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block, "no PEM block in %q", certPEM)
	c, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return c
}

func TestSaveSAMLConfig_GeneratesThePairOnTheFirstSave(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	firstSave(t, h, sess)

	cert, key := storedPair(t, h)
	require.NotEmpty(t, cert)
	_, err := tls.X509KeyPair([]byte(cert), []byte(key))
	require.NoError(t, err, "the stored certificate and key must be a pair")
	require.Equal(t, "localhost", parsePEMCert(t, cert).Subject.CommonName, "CN is the host of BASE_URL")

	got := adminGet(t, sess, samlPath)
	require.Equal(t, true, got["configured"])
	require.Equal(t, true, got["live"], "the generated pair loads SAML at once")
	require.Equal(t, cert, got["cert_pem"])
	require.Equal(t, true, got["cert_generated"])
	notAfter, err := time.Parse(time.RFC3339, got["cert_not_after"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().AddDate(10, 0, 0), notAfter, 24*time.Hour)
}

// Generation is for the save that has metadata and no pair, and nothing else.
func TestSaveSAMLConfig_GeneratesOnlyWhenThereIsMetadataAndNoPair(t *testing.T) {
	cases := []struct {
		name string
		// seeded is the stored cert and key before the request.
		seededCert, seededKey string
		body                  func(idpURL string) map[string]any
		wantStatus            int
		wantGenerated         bool
	}{
		{
			name: "metadata and nothing stored",
			body: func(u string) map[string]any { return map[string]any{"metadata_url": u} },
			// 204: SAML loads.
			wantStatus: http.StatusNoContent, wantGenerated: true,
		},
		{
			name: "metadata as XML",
			body: func(string) map[string]any {
				return map[string]any{"metadata_xml": samlIDPMetadataXML}
			},
			wantStatus: http.StatusNoContent, wantGenerated: true,
		},
		{
			// What the settings page sends on a first save before this change
			// reached it: the certificate field present and empty.
			name: "metadata with an empty certificate sent",
			body: func(u string) map[string]any {
				return map[string]any{"metadata_url": u, "cert_pem": ""}
			},
			wantStatus: http.StatusNoContent, wantGenerated: true,
		},
		{
			name: "no metadata",
			body: func(string) map[string]any { return map[string]any{} },
			// Nothing to configure, nothing generated: SAML is simply not loaded.
			wantStatus: http.StatusNoContent,
		},
		{
			name: "everything cleared",
			body: func(string) map[string]any {
				return map[string]any{"metadata_url": "", "metadata_xml": "", "cert_pem": "", "key_pem": ""}
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "a pair is stored",
			seededCert: validSAMLCert, seededKey: validSAMLKey,
			body:       func(u string) map[string]any { return map[string]any{"metadata_url": u} },
			wantStatus: http.StatusNoContent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
			if tc.seededCert != "" {
				require.NoError(t, h.adminSvc.SetSAMLConfig(context.Background(), "", tc.seededCert, tc.seededKey))
			}

			res, body := putSAML(t, sess, tc.body(idp.srv.URL))
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", body)

			cert, key := storedPair(t, h)
			if tc.wantGenerated {
				require.NotEmpty(t, cert)
				require.NotEmpty(t, key)
				require.NotEqual(t, validSAMLCert, cert)
				require.Len(t, h.entriesFor(t, "setting", samlCertEntity), 1)
			} else {
				require.Equal(t, tc.seededCert, cert, "a stored certificate is never replaced, and none appears from nothing")
				require.Equal(t, tc.seededKey, key)
				require.Empty(t, h.entriesFor(t, "setting", samlCertEntity))
			}
		})
	}
}

// A pair the administrator uploads works exactly as before, including a later
// save that says nothing about it.
func TestSaveSAMLConfig_AnUploadedPairIsKeptAndNeverRegeneratedBySaving(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")

	res, body := putSAML(t, sess, map[string]any{"metadata_url": idp.srv.URL, "cert_pem": validSAMLCert, "key_pem": validSAMLKey})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	for i := 0; i < 2; i++ {
		res, body = putSAML(t, sess, map[string]any{"metadata_url": idp.srv.URL})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	}

	cert, key := storedPair(t, h)
	require.Equal(t, validSAMLCert, cert)
	require.Equal(t, validSAMLKey, key)
	got := adminGet(t, sess, samlPath)
	require.Equal(t, false, got["cert_generated"], "an uploaded certificate is not called generated")
	require.NotNil(t, got["cert_not_after"], "the expiry is read off any certificate")
	require.Empty(t, h.entriesFor(t, "setting", samlCertEntity), "uploads are not audited, as before")
}

// A half-supplied pair is what it always was.
func TestSaveSAMLConfig_AHalfSuppliedPairIsStillRefused(t *testing.T) {
	cases := []struct {
		name       string
		seeded     bool // a generated pair is stored first
		body       map[string]any
		wantStored bool // the stored pair is what it was
	}{
		{name: "certificate without a key, nothing stored", body: map[string]any{"metadata_url": "https://idp.test/m", "cert_pem": validSAMLCert}},
		{name: "key without a certificate, nothing stored", body: map[string]any{"metadata_url": "https://idp.test/m", "key_pem": validSAMLKey}},
		{name: "a different key over a stored certificate", seeded: true, wantStored: true,
			body: map[string]any{"key_pem": validSAMLKey}},
		{name: "a different certificate over a stored key", seeded: true, wantStored: true,
			body: map[string]any{"cert_pem": validSAMLCert}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			if tc.seeded {
				firstSave(t, h, sess)
			}
			wantCert, wantKey := storedPair(t, h)

			res, body := putSAML(t, sess, tc.body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Contains(t, string(body), "invalid_cert_key")
			require.Contains(t, string(body), pairInvalid)

			cert, key := storedPair(t, h)
			require.Equal(t, wantCert, cert, "a refused save changes nothing, and generates nothing")
			require.Equal(t, wantKey, key)
		})
	}
}

func TestSaveSAMLConfig_RegenerateReplacesThePairAndReloadsSAML(t *testing.T) {
	cases := []struct {
		name   string
		upload bool // the pair being replaced was uploaded, not generated
	}{
		{name: "a generated pair"},
		{name: "an uploaded pair", upload: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
			seed := map[string]any{"metadata_url": idp.srv.URL}
			if tc.upload {
				seed["cert_pem"], seed["key_pem"] = validSAMLCert, validSAMLKey
			}
			res, body := putSAML(t, sess, seed)
			require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
			oldCert, oldKey := storedPair(t, h)
			wantEntries := 1
			if tc.upload {
				wantEntries = 0
			}
			require.Len(t, h.entriesFor(t, "setting", samlCertEntity), wantEntries)

			res, body = putSAML(t, sess, map[string]any{"regenerate_cert": true})
			require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

			cert, key := storedPair(t, h)
			require.NotEqual(t, oldCert, cert)
			require.NotEqual(t, oldKey, key)
			_, err := tls.X509KeyPair([]byte(cert), []byte(key))
			require.NoError(t, err)

			got := adminGet(t, sess, samlPath)
			require.Equal(t, cert, got["cert_pem"])
			require.Equal(t, true, got["cert_generated"])
			require.Equal(t, true, got["live"])

			// Reloaded: the SP metadata the running middleware serves carries the
			// new certificate, not the old one.
			meta := h.rawGet(t, "/api/v1/auth/saml/metadata", nil)
			require.Equal(t, http.StatusOK, meta.StatusCode)
			metaBody := readBody(t, meta)
			newDER := base64.StdEncoding.EncodeToString(parsePEMCert(t, cert).Raw)
			oldDER := base64.StdEncoding.EncodeToString(parsePEMCert(t, oldCert).Raw)
			require.Contains(t, metaBody, newDER, "SAML must be rebuilt on the new pair")
			require.NotContains(t, metaBody, oldDER)

			entries := h.entriesFor(t, "setting", samlCertEntity)
			require.Len(t, entries, wantEntries+1)
			require.Equal(t, "saml_cert_regenerated", entries[0].Action, "newest first")
		})
	}
}

// One request, one source for the pair.
func TestSaveSAMLConfig_RegenerateCannotBeCombinedWithAnUpload(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"with a certificate", map[string]any{"regenerate_cert": true, "cert_pem": validSAMLCert}},
		{"with a key", map[string]any{"regenerate_cert": true, "key_pem": validSAMLKey}},
		{"with both", map[string]any{"regenerate_cert": true, "cert_pem": validSAMLCert, "key_pem": validSAMLKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := adminSession(t, h)
			firstSave(t, h, sess)
			wantCert, _ := storedPair(t, h)

			res, body := putSAML(t, sess, tc.body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Contains(t, string(body), "invalid_cert_key")
			cert, _ := storedPair(t, h)
			require.Equal(t, wantCert, cert)
		})
	}
}

// regenerate_cert: false is the field absent.
func TestSaveSAMLConfig_RegenerateFalseDoesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	firstSave(t, h, sess)
	wantCert, _ := storedPair(t, h)

	res, body := putSAML(t, sess, map[string]any{"regenerate_cert": false})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	cert, _ := storedPair(t, h)
	require.Equal(t, wantCert, cert)
}

// Two saves at once on a fresh install make one pair, and both see it.
func TestSaveSAMLConfig_GeneratesOnceWhenSavesRace(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")

	const racers = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	statuses := make([]int, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Each racer is its own browser: a response replaces the cookie jar.
			own := &session{h: h, jar: append([]*http.Cookie(nil), sess.jar...)}
			res, _ := own.send(t, http.MethodPut, samlPath, map[string]any{"metadata_url": idp.srv.URL})
			statuses[i] = res.StatusCode
		}()
	}
	close(start)
	wg.Wait()

	for _, s := range statuses {
		require.Equal(t, http.StatusNoContent, s)
	}
	require.Len(t, h.entriesFor(t, "setting", samlCertEntity), 1, "one generation, however many saves raced")
	cert, key := storedPair(t, h)
	_, err := tls.X509KeyPair([]byte(cert), []byte(key))
	require.NoError(t, err, "the certificate and key come from the same generation")
}

// The private key is in the database and in the running middleware, and nowhere
// a request or an administrator can read it.
func TestSaveSAMLConfig_TheKeyIsNeverInAResponseOrTheAuditLog(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")

	var seen []string
	res, body := putSAML(t, sess, map[string]any{"metadata_url": idp.srv.URL})
	seen = append(seen, string(body))
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	res, body = putSAML(t, sess, map[string]any{"regenerate_cert": true})
	seen = append(seen, string(body))
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	for _, path := range []string{samlPath, "/api/v1/admin/settings", "/api/v1/admin/audit"} {
		res, body := sess.send(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s: %s", path, body)
		seen = append(seen, string(body))
	}
	meta := h.rawGet(t, "/api/v1/auth/saml/metadata", nil)
	seen = append(seen, readBody(t, meta))

	_, key := storedPair(t, h)
	block, _ := pem.Decode([]byte(key))
	require.NotNil(t, block)
	keyB64 := base64.StdEncoding.EncodeToString(block.Bytes)
	// A distinctive stretch of the key, past the PEM header and its line breaks.
	needle := keyB64[40:80]
	for _, s := range seen {
		require.NotContains(t, s, "PRIVATE KEY")
		require.NotContains(t, strings.ReplaceAll(s, "\\n", ""), needle)
	}

	raw := h.rawAudit(t, samlCertEntity)
	require.NotEmpty(t, raw)
	require.NotContains(t, raw, "PRIVATE KEY")
	require.NotContains(t, raw, needle)
	require.Contains(t, raw, "cert_fingerprint")
}

func TestSaveSAMLConfig_TheAuditEntryNamesTheActorAndTheCertificate(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)
	firstSave(t, h, sess)

	entries := h.entriesFor(t, "setting", samlCertEntity)
	require.Len(t, entries, 1)
	e := entries[0]
	require.Equal(t, "saml_cert_generated", e.Action)
	require.NotNil(t, e.ActorID)
	require.Equal(t, h.adminID, *e.ActorID)
	require.Nil(t, e.Before)
	require.Equal(t, "generated", e.After["source"])
	cert, _ := storedPair(t, h)
	c := parsePEMCert(t, cert)
	require.Equal(t, c.NotAfter.UTC().Format(time.RFC3339), e.After["cert_not_after"])
	require.NotEmpty(t, e.After["cert_fingerprint"])
	require.Len(t, e.After, 3, "source, cert_not_after, cert_fingerprint: nothing else is written")
}

// GET /admin/saml with nothing stored.
func TestGetSAMLConfig_WithNoCertificate(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := adminSession(t, h)

	got := adminGet(t, sess, samlPath)
	require.Equal(t, "", got["cert_pem"])
	require.Nil(t, got["cert_not_after"])
	require.Equal(t, false, got["cert_generated"])
}
