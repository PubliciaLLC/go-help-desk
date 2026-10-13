package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/stretchr/testify/require"
)

// samlSPMetadataLiteral is a service provider's metadata, which is what this
// instance serves at /api/v1/auth/saml/metadata and shows on the same
// settings page: the most likely thing to be pasted by mistake (#461).
const samlSPMetadataLiteral = `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://hd/api/v1/auth/saml/metadata"><SPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"><AssertionConsumerService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="https://hd/acs" index="1"/></SPSSODescriptor></EntityDescriptor>`

// getSAMLConfig reads GET /admin/saml as the signed-in admin.
func getSAMLConfig(t *testing.T, sess *session) map[string]any {
	t.Helper()
	res, body := sess.send(t, http.MethodGet, "/api/v1/admin/saml", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

// samlErrorCode extracts {error:{code}} from a 4xx body.
func samlErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &e), "body: %s", body)
	return e.Error.Code
}

// xxeDocument is the fixture with an external entity declared and referenced
// from an attribute, pointing at system.
func xxeDocument(system string) string {
	x := strings.Replace(samlIDPMetadataXML, "<EntityDescriptor ",
		`<!DOCTYPE EntityDescriptor [<!ENTITY xxe SYSTEM "`+system+`">]><EntityDescriptor `, 1)
	return strings.Replace(x,
		`Location="https://idp.testshib.org/idp/profile/SAML2/Redirect/SSO"`, `Location="&xxe;"`, 1)
}

func TestSaveSAMLConfig_MetadataXML(t *testing.T) {
	var xxeHits atomic.Int64
	xxe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		xxeHits.Add(1)
	}))
	defer xxe.Close()

	const storedURL = "https://idp.test/metadata"
	seedURL := func(t *testing.T, h *harness) {
		require.NoError(t, h.adminSvc.SetSAMLConfig(context.Background(), storedURL, validSAMLCert, validSAMLKey))
	}
	over := samlIDPMetadataXML + "<!--" + strings.Repeat("x", auth.MaxIdPMetadataXMLBytes) + "-->"

	cases := []struct {
		name       string
		seed       func(*testing.T, *harness)
		body       map[string]any
		wantStatus int
		wantCode   string
		wantURL    string
		wantXML    string
	}{
		{
			name:       "switching from URL to XML in one save",
			seed:       seedURL,
			body:       map[string]any{"metadata_url": "", "metadata_xml": samlIDPMetadataXML},
			wantStatus: http.StatusNoContent,
			wantXML:    samlIDPMetadataXML,
		},
		{
			name:       "XML sent while a URL is stored",
			seed:       seedURL,
			body:       map[string]any{"metadata_xml": samlIDPMetadataXML},
			wantStatus: http.StatusBadRequest,
			wantCode:   "saml_metadata_conflict",
			wantURL:    storedURL,
		},
		{
			name: "both sent",
			body: map[string]any{
				"metadata_url": storedURL, "metadata_xml": samlIDPMetadataXML,
				"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "saml_metadata_conflict",
		},
		{
			name: "not XML",
			body: map[string]any{
				"metadata_url": "", "metadata_xml": "hello",
				"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_saml_metadata",
		},
		{
			name: "SP metadata pasted",
			body: map[string]any{
				"metadata_url": "", "metadata_xml": samlSPMetadataLiteral,
				"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_saml_metadata",
		},
		{
			name: "over 256 KiB",
			body: map[string]any{
				"metadata_url": "", "metadata_xml": over,
				"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "saml_metadata_too_large",
		},
		{
			name: "external entity",
			body: map[string]any{
				"metadata_url": "", "metadata_xml": xxeDocument(xxe.URL + "/x"),
				"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_saml_metadata",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := signInAsAdmin(t, h)
			if tc.seed != nil {
				tc.seed(t, h)
			}

			res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", tc.body)
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", body)
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, samlErrorCode(t, body))
			}

			url, _, _ := h.adminSvc.GetSAMLConfig(context.Background())
			require.Equal(t, tc.wantURL, url, "stored metadata URL")
			require.Equal(t, tc.wantXML, h.adminSvc.GetSAMLMetadataXML(context.Background()), "stored metadata XML")
			if tc.wantStatus == http.StatusBadRequest && tc.seed == nil {
				_, cert, key := h.adminSvc.GetSAMLConfig(context.Background())
				require.Empty(t, cert, "a refused save must store nothing")
				require.Empty(t, key, "a refused save must store nothing")
			}
		})
	}
	require.Zero(t, xxeHits.Load(), "a pasted external entity must never be fetched")
}

func TestSaveSAMLConfig_PastedMetadataLoadsAndReadsBack(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)

	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "", "metadata_xml": samlIDPMetadataXML,
		"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	got := getSAMLConfig(t, sess)
	require.Equal(t, samlIDPMetadataXML, got["metadata_xml"], "the XML reads back byte for byte")
	require.Equal(t, "", got["metadata_url"])
	require.Equal(t, true, got["configured"])
	require.NotContains(t, got, "key_pem", "the private key stays write-only")

	resp := h.rawGet(t, "/api/v1/auth/saml/metadata", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "SP metadata is served only once SAML has loaded")

	// An omitted metadata_xml keeps it.
	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{"cert_pem": validSAMLCert})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, samlIDPMetadataXML, h.adminSvc.GetSAMLMetadataXML(context.Background()))

	// An explicit empty string clears it. The seeded local admin has a
	// password, so nobody is stranded and the answer is a plain 204.
	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{"metadata_xml": ""})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	require.Equal(t, "", h.adminSvc.GetSAMLMetadataXML(context.Background()))
	got = getSAMLConfig(t, sess)
	require.Equal(t, false, got["configured"])
	require.Equal(t, "", got["metadata_xml"])
}

// Startup loads stored pasted metadata with no network involved.
func TestInitSAML_LoadsStoredMetadataXML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "", validSAMLCert, validSAMLKey))
	require.NoError(t, h.adminSvc.SetSAMLMetadataXML(ctx, samlIDPMetadataXML))
	h.srv.InitSAML(ctx)

	resp := h.rawGet(t, "/api/v1/auth/saml/metadata", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// The lockout guard (#300) counts pasted metadata as a way to sign in: a
// SAML-only administrator is not stranded by a save that leaves SAML loaded
// from XML, and is named when that same XML is cleared.
func TestSaveSAMLConfig_GuardTreatsPastedMetadataAsReachable(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "saml-admin@test.local",
		DisplayName: "SAML Admin",
		Role:        user.RoleAdmin,
		SAMLSubject: "saml-admin-sub",
	})
	require.NoError(t, err)
	sess := signInAsAdmin(t, h)

	res, body := sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{
		"metadata_url": "", "metadata_xml": samlIDPMetadataXML,
		"cert_pem": validSAMLCert, "key_pem": validSAMLKey,
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode,
		"pasted metadata that loads is reachable, so nobody is stranded; body: %s", body)

	res, body = sess.send(t, http.MethodPut, "/api/v1/admin/saml", map[string]any{"metadata_xml": ""})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "saml-admin@test.local",
		"the warning must name the administrator who would be stranded")
	require.Equal(t, "", h.adminSvc.GetSAMLMetadataXML(ctx))
}
