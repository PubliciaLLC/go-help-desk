package server

import (
	"testing"

	"github.com/crewjam/saml/samlsp"
	"github.com/stretchr/testify/require"
)

// The email and display name a SAML sign-in gives the account (#461). Lookup
// is by attribute Name; Entra ID, Google and Okta send no FriendlyName for
// these, so the library keys them by the full URI or the configured name.
func TestSAMLIdentity(t *testing.T) {
	const (
		oidMail   = "urn:oid:0.9.2342.19200300.100.1.3"
		e         = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/"
		entraName = "http://schemas.microsoft.com/identity/claims/displayname"
	)
	cases := []struct {
		name      string
		nameID    string
		attrs     map[string][]string
		wantEmail string
		wantName  string
	}{
		{"displayName wins", "sub",
			map[string][]string{"email": {"a@x"}, "displayName": {"Ada L"}, "givenName": {"Ada"}, "sn": {"Lovelace"}},
			"a@x", "Ada L"},
		{"cn when no displayName (crewjam fake IdP shape)", "sub",
			map[string][]string{"mail": {"a@x"}, "cn": {"SSO Person"}},
			"a@x", "SSO Person"},
		{"givenName and sn (Okta and Google, 1.3 docs)", "a@x",
			map[string][]string{"email": {"a@x"}, "givenName": {"Ada"}, "sn": {"Lovelace"}},
			"a@x", "Ada Lovelace"},
		{"LDAP mail OID", "sub",
			map[string][]string{oidMail: {"a@x"}},
			"a@x", "a@x"},
		{"no email attribute, email NameID", "a@x",
			map[string][]string{},
			"a@x", "a@x"},
		{"given name alone", "a@x",
			map[string][]string{"givenName": {"Ada"}},
			"a@x", "Ada"},
		{"blank values skipped", "sub",
			map[string][]string{"email": {"  "}, "mail": {"a@x"}, "displayName": {"  "}, "cn": {"Ada"}},
			"a@x", "Ada"},
		{"Entra 1.3 docs (custom email/displayName, no namespace)", "upn@c",
			map[string][]string{"email": {"a@x"}, "displayName": {"Ada Lovelace"}},
			"a@x", "Ada Lovelace"},
		// New in #461.
		{"firstName and lastName (1.2 docs, Okta and Google)", "a@x",
			map[string][]string{"email": {"a@x"}, "firstName": {"Ada"}, "lastName": {"Lovelace"}},
			"a@x", "Ada Lovelace"},
		{"lastName alone", "a@x",
			map[string][]string{"lastName": {"Lovelace"}},
			"a@x", "Lovelace"},
		{"givenName preferred over firstName", "a@x",
			map[string][]string{"givenName": {"Ada"}, "firstName": {"Augusta"}, "sn": {"Lovelace"}},
			"a@x", "Ada Lovelace"},
		{"mixed givenName with lastName", "a@x",
			map[string][]string{"givenName": {"Ada"}, "lastName": {"Lovelace"}},
			"a@x", "Ada Lovelace"},
		// The name claim is the user principal name in Entra by default, so it
		// ranks below given name plus surname: otherwise every Entra user would
		// get their UPN as display name, the bug being fixed.
		{"Entra default claims", "ada@contoso.onmicrosoft.com",
			map[string][]string{e + "emailaddress": {"ada@contoso.com"}, e + "givenname": {"Ada"}, e + "surname": {"Lovelace"}, e + "name": {"ada@contoso.onmicrosoft.com"}},
			"ada@contoso.com", "Ada Lovelace"},
		{"Entra display name claim", "upn@c",
			map[string][]string{entraName: {"Ada L."}, e + "givenname": {"Ada"}, e + "surname": {"Lovelace"}},
			"upn@c", "Ada L."},
		{"Entra user without a mailbox", "upn@c",
			map[string][]string{e + "givenname": {"Ada"}, e + "surname": {"Lovelace"}, e + "name": {"upn@c"}},
			"upn@c", "Ada Lovelace"},
		{"only the name claim", "upn@c",
			map[string][]string{e + "name": {"Ada Lovelace"}},
			"upn@c", "Ada Lovelace"},
		{"1.2 Azure mapping (emailaddress, givenname, surname mapped to email, firstName, lastName)", "upn@c",
			map[string][]string{"email": {"a@x"}, "firstName": {"Ada"}, "lastName": {"Lovelace"}},
			"a@x", "Ada Lovelace"},
		{"email attribute beats the Entra URI", "sub",
			map[string][]string{"email": {"a@x"}, e + "emailaddress": {"b@x"}},
			"a@x", "a@x"},
		{"Entra URI beats the NameID", "upn@c",
			map[string][]string{e + "emailaddress": {"mail@c"}},
			"mail@c", "mail@c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			email, name := samlIdentity(tc.nameID, samlsp.Attributes(tc.attrs))
			require.Equal(t, tc.wantEmail, email, "email")
			require.Equal(t, tc.wantName, name, "display name")
		})
	}
}
