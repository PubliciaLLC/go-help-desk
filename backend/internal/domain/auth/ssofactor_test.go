package auth

import (
	"encoding/json"
	"testing"
)

const (
	testAuthnAttr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
	testMultiple  = "http://schemas.microsoft.com/claims/multipleauthn"
	testPassword  = "http://schemas.microsoft.com/ws/2008/06/identity/authenticationmethod/password"
)

// An SSO sign-in counts as having proved a second factor only when the
// provider says so (#333). For SAML from Entra ID that is
// http://schemas.microsoft.com/claims/multipleauthn under
// authnmethodsreferences, which Entra sends only after MFA.
func TestSAMLAssertedMFA(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string][]string
		want  bool
	}{
		{"entra after MFA", map[string][]string{testAuthnAttr: {testPassword, testMultiple}}, true},
		{"entra, password only", map[string][]string{testAuthnAttr: {testPassword}}, false},
		{"no attribute (amr not configured)", map[string][]string{}, false},
		{"value under another attribute", map[string][]string{"role": {testMultiple}}, false},
		{"bare word, not the claim URI", map[string][]string{testAuthnAttr: {"multipleauthn"}}, false},
		{"filed under a FriendlyName", map[string][]string{"authnmethodsreferences": {testMultiple}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SAMLAssertedMFA(tc.attrs); got != tc.want {
				t.Fatalf("SAMLAssertedMFA = %v, want %v", got, tc.want)
			}
		})
	}
}

// #616 part c. Only values Microsoft documents for Entra count as a passkey;
// nothing is guessed for Okta or Google Workspace.
func TestSSOPasskeyGrade(t *testing.T) {
	t.Run("oidc", func(t *testing.T) {
		cases := []struct {
			raw  string
			want bool
		}{
			{`{"amr":["fido"]}`, true},
			{`{"amr":["hwk","mfa"]}`, true},
			{`{"amr":["pwd","fido","mfa"]}`, true},
			{`{"amr":"fido"}`, true},
			{`{"amr":["mfa"]}`, false},
			{`{"amr":["pwd"]}`, false},
			{`{"amr":["FIDO"]}`, false},
			{`{"amr":["phr"]}`, false},
			{`{"amr":["webauthn"]}`, false},
			{`{}`, false},
		}
		for _, tc := range cases {
			var c OIDCClaims
			if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
				t.Fatalf("%s: %v", tc.raw, err)
			}
			if got := c.AssertedPasskey(); got != tc.want {
				t.Fatalf("%s: AssertedPasskey = %v, want %v", tc.raw, got, tc.want)
			}
		}
	})
	t.Run("saml", func(t *testing.T) {
		cases := []struct {
			name  string
			attrs map[string][]string
			want  bool
		}{
			{"fido with multipleauthn", map[string][]string{testAuthnAttr: {"fido", testMultiple}}, true},
			{"hwk with multipleauthn", map[string][]string{testAuthnAttr: {testMultiple, "hwk"}}, true},
			{"friendly name key", map[string][]string{"authnmethodsreferences": {"fido", testMultiple}}, true},
			{"fido alone", map[string][]string{testAuthnAttr: {"fido"}}, false},
			{"multipleauthn alone", map[string][]string{testAuthnAttr: {testMultiple}}, false},
			{"fido under another attribute", map[string][]string{"role": {"fido", testMultiple}}, false},
			{"okta style value", map[string][]string{testAuthnAttr: {"urn:okta:loa:2fa:any", testMultiple}}, false},
			{"phr", map[string][]string{testAuthnAttr: {"phr", testMultiple}}, false},
			{"webauthn", map[string][]string{testAuthnAttr: {"webauthn", testMultiple}}, false},
			{"empty", map[string][]string{}, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := SAMLAssertedPasskey(tc.attrs); got != tc.want {
					t.Fatalf("SAMLAssertedPasskey = %v, want %v", got, tc.want)
				}
			})
		}
	})
}
