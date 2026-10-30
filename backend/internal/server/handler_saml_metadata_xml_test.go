package server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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
	notX509 := regexp.MustCompile(`(?s)(<ds:X509Certificate>).*?(</ds:X509Certificate>)`).
		ReplaceAllString(samlIDPMetadataXML, "${1}bm90IGEgY2VydA==${2}")

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
			// Loads, but no assertion could ever be verified against it, so a
			// SAML-only administrator would be stranded behind a 204.
			name: "signing certificate is not X.509",
			body: map[string]any{
				"metadata_url": "", "metadata_xml": notX509,
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

// The generic settings door refuses the same things PUT /admin/saml does, as
// 400 bad_request (the way it reports every SSO refusal), and returns the XML
// in the dump with no "_set" flag: it is public data, not a secret.
func TestUpdateSettings_SAMLMetadataXML(t *testing.T) {
	const storedURL = "https://idp.test/metadata"
	seedURL := func(t *testing.T, h *harness) {
		require.NoError(t, h.adminSvc.SetSAMLConfig(context.Background(), storedURL, validSAMLCert, validSAMLKey))
	}
	cases := []struct {
		name       string
		seed       func(*testing.T, *harness)
		body       map[string]any
		wantStatus int
		wantXML    string
	}{
		{"valid XML with the URL cleared", seedURL,
			map[string]any{admin.KeySAMLMetadataURL: "", admin.KeySAMLMetadataXML: samlIDPMetadataXML},
			http.StatusNoContent, samlIDPMetadataXML},
		{"conflict with the stored URL", seedURL,
			map[string]any{admin.KeySAMLMetadataXML: samlIDPMetadataXML},
			http.StatusBadRequest, ""},
		{"invalid", nil,
			map[string]any{admin.KeySAMLMetadataXML: "hello"},
			http.StatusBadRequest, ""},
		{"SP metadata pasted", nil,
			map[string]any{admin.KeySAMLMetadataXML: samlSPMetadataLiteral},
			http.StatusBadRequest, ""},
		{"null", nil,
			map[string]any{admin.KeySAMLMetadataXML: nil},
			http.StatusBadRequest, ""},
		{"wrong type", nil,
			map[string]any{admin.KeySAMLMetadataXML: 5},
			http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := signInAsAdmin(t, h)
			if tc.seed != nil {
				tc.seed(t, h)
			}

			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", tc.body)
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", body)
			if tc.wantStatus == http.StatusBadRequest {
				require.Equal(t, "bad_request", samlErrorCode(t, body))
			}
			require.Equal(t, tc.wantXML, h.adminSvc.GetSAMLMetadataXML(context.Background()), "stored metadata XML")

			if tc.wantXML != "" {
				res, body = sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
				require.Equal(t, http.StatusOK, res.StatusCode)
				var dump map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &dump))
				var got string
				require.NoError(t, json.Unmarshal(dump[admin.KeySAMLMetadataXML], &got))
				require.Equal(t, tc.wantXML, got)
				require.NotContains(t, dump, admin.KeySAMLMetadataXML+"_set", "public data carries no presence flag")
			}
		})
	}
}

// An API key is a machine credential: repointing the IdP is a route to a
// session, so it stays session-only (AuthCriticalKeys).
func TestUpdateSettings_SAMLMetadataXMLRequiresASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeySAMLMetadataXML: samlIDPMetadataXML})
	defer res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	require.Equal(t, "session_required", body.Error.Code)
	require.Equal(t, "", h.adminSvc.GetSAMLMetadataXML(context.Background()))
}

// TestSAMLSignIn_EndToEndWithPastedMetadataXML is
// TestSAMLSignIn_EndToEndWithASignedAssertion with the IdP configured from
// pasted metadata instead of a URL, and with the IdP's metadata endpoint shut
// down before SAML loads, so nothing can have been fetched. The signed
// assertion is still verified for real against the pasted signing certificate.
func TestSAMLSignIn_EndToEndWithPastedMetadataXML(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
	b, err := xml.Marshal(idp.idp.Metadata())
	require.NoError(t, err)
	idp.srv.Close()

	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "", validSAMLCert, validSAMLKey))
	require.NoError(t, h.adminSvc.SetSAMLMetadataXML(ctx, string(b)))
	h.srv.InitSAML(ctx)

	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	var tracking *http.Cookie
	for _, c := range res.Cookies() {
		if strings.HasPrefix(c.Name, "saml_") {
			tracking = c
		}
	}
	require.NotNil(t, tracking)

	form := idp.sign(t, res.Header.Get("Location"), "sso-xml@test.local")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(tracking)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	res = rr.Result()
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode, "ACS body: %s", rr.Body.String())
	require.Equal(t, "/api/v1/auth/saml/complete", res.Header.Get("Location"))
	var token *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "token" && c.Value != "" {
			token = c
		}
	}
	require.NotNil(t, token)

	res = h.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{token})
	res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"))
	var app *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name != "token" && c.Value != "" {
			app = c
		}
	}
	require.NotNil(t, app, "an app session cookie is issued")
	me := h.doUnauthWithCookie(t, http.MethodGet, "/api/v1/me", app)
	me.Body.Close()
	require.Equal(t, http.StatusOK, me.StatusCode)
}

// samlSignInWithClaims runs a SAML sign-in against idp (configured from pasted
// metadata) whose assertion carries exactly attrs, each keyed by its Name with
// no FriendlyName, the way Entra ID, Google and Okta send them. It returns the
// ACS answer and, when the ACS accepted, the /complete answer.
func samlSignInWithClaims(t *testing.T, h *harness, idp *fakeIdP, nameID string, attrs map[string]string) (acs *httptest.ResponseRecorder, complete *http.Response) {
	t.Helper()
	res := h.rawGet(t, "/api/v1/auth/saml/login", nil)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	var tracking *http.Cookie
	for _, c := range res.Cookies() {
		if strings.HasPrefix(c.Name, "saml_") {
			tracking = c
		}
	}
	require.NotNil(t, tracking)

	req, err := saml.NewIdpAuthnRequest(idp.idp, httptest.NewRequest(http.MethodGet, res.Header.Get("Location"), nil))
	require.NoError(t, err)
	require.NoError(t, req.Validate())
	require.NoError(t, saml.DefaultAssertionMaker{}.MakeAssertion(req, &saml.Session{ID: "s1", NameID: nameID}))
	var stmt saml.AttributeStatement
	for name, v := range attrs {
		stmt.Attributes = append(stmt.Attributes, saml.Attribute{
			Name:       name,
			NameFormat: "urn:oasis:names:tc:SAML:2.0:attrname-format:uri",
			Values:     []saml.AttributeValue{{Type: "xs:string", Value: v}},
		})
	}
	req.Assertion.AttributeStatements = []saml.AttributeStatement{stmt}
	form, err := req.PostBinding()
	require.NoError(t, err)

	post := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs",
		strings.NewReader(url.Values{"SAMLResponse": {form.SAMLResponse}, "RelayState": {form.RelayState}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(tracking)
	acs = httptest.NewRecorder()
	h.srv.ServeHTTP(acs, post)
	if acs.Code != http.StatusFound {
		return acs, nil
	}
	for _, c := range acs.Result().Cookies() {
		if c.Name == "token" && c.Value != "" {
			complete = h.rawGet(t, "/api/v1/auth/saml/complete", []*http.Cookie{c})
			complete.Body.Close()
		}
	}
	require.NotNil(t, complete, "the ACS accepted but issued no hand-over token")
	return acs, complete
}

// configurePastedIdP stores md as pasted metadata and loads SAML from it.
func configurePastedIdP(t *testing.T, h *harness, md *saml.EntityDescriptor) {
	t.Helper()
	ctx := context.Background()
	b, err := xml.Marshal(md)
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetSAMLConfig(ctx, "", validSAMLCert, validSAMLKey))
	require.NoError(t, h.adminSvc.SetSAMLMetadataXML(ctx, string(b)))
	h.srv.InitSAML(ctx)
}

// The sign-in handler takes the account's email and name from samlIdentity
// (#461). TestSAMLIdentity pins the precedence; this pins the wiring, and that
// the library really keys a claim with no FriendlyName by its full URI.
func TestSAMLSignIn_StoresTheNameAndEmailTheClaimsGive(t *testing.T) {
	const e = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/"
	cases := []struct {
		name      string
		nameID    string
		attrs     map[string]string
		wantEmail string
		wantName  string
	}{
		{"Entra ID default claims", "ada@contoso.onmicrosoft.com",
			map[string]string{e + "emailaddress": "ada@contoso.test", e + "givenname": "Ada", e + "surname": "Lovelace", e + "name": "ada@contoso.onmicrosoft.com"},
			"ada@contoso.test", "Ada Lovelace"},
		{"firstName and lastName (1.2 docs)", "sub-ada",
			map[string]string{"email": "ada@okta.test", "firstName": "Ada", "lastName": "Lovelace"},
			"ada@okta.test", "Ada Lovelace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")
			configurePastedIdP(t, h, idp.idp.Metadata())

			acs, complete := samlSignInWithClaims(t, h, idp, tc.nameID, tc.attrs)
			require.Equal(t, http.StatusFound, acs.Code, "ACS body: %s", acs.Body.String())
			require.Equal(t, http.StatusSeeOther, complete.StatusCode)
			require.Equal(t, "/", complete.Header.Get("Location"))

			u, err := h.userSvc.GetByEmail(context.Background(), tc.wantEmail)
			require.NoError(t, err)
			require.Equal(t, tc.wantName, u.DisplayName)
			require.Equal(t, tc.nameID, u.SAMLSubject)
		})
	}
}

// Pasted metadata is the trust anchor: an assertion signed by any key other
// than the one its signing certificate names is refused, and nobody is
// signed in or provisioned.
func TestSAMLSignIn_PastedMetadataRefusesAnotherKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	idp := newFakeIdP(t, h.cfgBaseURL()+"/api/v1/auth/saml/acs")

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "not the signing IdP"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &other.PublicKey, other)
	require.NoError(t, err)
	md := idp.idp.Metadata()
	replaced := 0
	for i := range md.IDPSSODescriptors {
		for j := range md.IDPSSODescriptors[i].KeyDescriptors {
			certs := md.IDPSSODescriptors[i].KeyDescriptors[j].KeyInfo.X509Data.X509Certificates
			for k := range certs {
				certs[k].Data = base64.StdEncoding.EncodeToString(der)
				replaced++
			}
		}
	}
	require.NotZero(t, replaced)
	configurePastedIdP(t, h, md)

	acs, complete := samlSignInWithClaims(t, h, idp, "intruder-sub", map[string]string{"email": "intruder@test.local"})
	require.Equal(t, http.StatusForbidden, acs.Code, "ACS body: %s", acs.Body.String())
	require.Nil(t, complete)
	for _, c := range acs.Result().Cookies() {
		require.NotEqual(t, "token", c.Name, "no hand-over token for an assertion signed by another key")
	}
	_, err = h.userSvc.GetByEmail(context.Background(), "intruder@test.local")
	require.ErrorIs(t, err, user.ErrNotFound, "nobody is provisioned")
}
