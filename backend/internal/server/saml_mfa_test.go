package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An SSO sign-in counts as having proved a second factor only when the
// provider says so (#333). For SAML from Entra ID that is
// http://schemas.microsoft.com/claims/multipleauthn under
// authnmethodsreferences, which Entra sends only after MFA.
func TestSAMLAssertedMFA(t *testing.T) {
	const attr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
	cases := []struct {
		name  string
		attrs map[string][]string
		want  bool
	}{
		{"entra after MFA", map[string][]string{attr: {
			"http://schemas.microsoft.com/ws/2008/06/identity/authenticationmethod/password",
			"http://schemas.microsoft.com/claims/multipleauthn",
		}}, true},
		{"entra, password only", map[string][]string{attr: {
			"http://schemas.microsoft.com/ws/2008/06/identity/authenticationmethod/password",
		}}, false},
		{"no attribute (amr not configured)", map[string][]string{}, false},
		{"value under another attribute", map[string][]string{"role": {"http://schemas.microsoft.com/claims/multipleauthn"}}, false},
		{"bare word, not the claim URI", map[string][]string{attr: {"multipleauthn"}}, false},
		{"filed under a FriendlyName", map[string][]string{"authnmethodsreferences": {
			"http://schemas.microsoft.com/claims/multipleauthn",
		}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, samlAssertedMFA(tc.attrs))
		})
	}
}
