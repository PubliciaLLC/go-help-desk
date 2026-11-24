package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

func nameIDMiddleware(t *testing.T) *samlsp.Middleware {
	t.Helper()
	certPEM, keyPEM := selfSignedSP(t)
	mw, err := auth.NewSAMLMiddleware(context.Background(), auth.SAMLConfig{
		BaseURL: "https://helpdesk.example.com", MetadataXML: []byte(samlIDPMetadataXML),
		CertPEM: certPEM, KeyPEM: keyPEM,
	})
	require.NoError(t, err)
	return mw
}

// The library asks for a transient NameID unless told otherwise, and Entra
// answers that with a random value at every sign-in. The subject is the
// account's key, so every account worked for exactly one sign-in.
func TestNewSAMLMiddleware_AsksForAPersistentNameID(t *testing.T) {
	mw := nameIDMiddleware(t)

	req, err := mw.ServiceProvider.MakeAuthenticationRequest(
		"https://idp.example/sso", saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	require.NoError(t, err)

	require.NotNil(t, req.NameIDPolicy)
	require.NotNil(t, req.NameIDPolicy.Format)
	require.Equal(t, string(saml.PersistentNameIDFormat), *req.NameIDPolicy.Format)

	var formats []string
	for _, f := range mw.ServiceProvider.Metadata().SPSSODescriptors[0].NameIDFormats {
		formats = append(formats, string(f))
	}
	require.Contains(t, formats, string(saml.PersistentNameIDFormat), "the SP metadata advertises what it asks for")
}

func responseWithStatus(top, sub string) string {
	nested := ""
	if sub != "" {
		nested = `<samlp:StatusCode Value="` + sub + `"/>`
	}
	return `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ID="_r" Version="2.0">` +
		`<samlp:Status><samlp:StatusCode Value="` + top + `">` + nested + `</samlp:StatusCode></samlp:Status></samlp:Response>`
}

// An identity provider that refuses persistent answers with a status error.
// The library's default is a bare 403 page; the administrator gets a message
// the login page can show instead, and nothing else changes.
func TestACSError_NameIDPolicyRefusalRedirectsToTheLoginPage(t *testing.T) {
	mw := nameIDMiddleware(t)
	requester := "urn:oasis:names:tc:SAML:2.0:status:Requester"
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantLoc  string
	}{
		{"InvalidNameIDPolicy",
			&saml.InvalidResponseError{PrivateErr: saml.ErrBadStatus{Status: requester},
				Response: responseWithStatus(requester, "urn:oasis:names:tc:SAML:2.0:status:InvalidNameIDPolicy")},
			http.StatusSeeOther, "/login?error=saml_nameid_format"},
		{"another status error keeps the library's 403",
			&saml.InvalidResponseError{PrivateErr: saml.ErrBadStatus{Status: requester},
				Response: responseWithStatus(requester, "urn:oasis:names:tc:SAML:2.0:status:AuthnFailed")},
			http.StatusForbidden, ""},
		{"a nested code in a response that failed for another reason is not trusted",
			&saml.InvalidResponseError{PrivateErr: errors.New("signature invalid"),
				Response: responseWithStatus(requester, "urn:oasis:names:tc:SAML:2.0:status:InvalidNameIDPolicy")},
			http.StatusForbidden, ""},
		{"a response that is not XML", &saml.InvalidResponseError{PrivateErr: errors.New("x"), Response: "garbage"},
			http.StatusForbidden, ""},
		{"an error that is not a response error", errors.New("boom"), http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mw.OnError(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", nil), tc.err)
			require.Equal(t, tc.wantCode, rec.Code)
			if tc.wantLoc != "" {
				require.Equal(t, tc.wantLoc, rec.Header().Get("Location"))
			}
		})
	}
}
