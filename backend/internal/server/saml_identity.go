package server

import (
	"github.com/crewjam/saml/samlsp"
)

// samlIdentity is the email and display name a SAML sign-in gives the account.
func samlIdentity(nameID string, a samlsp.Attributes) (email, displayName string) {
	email = firstNonEmpty(
		a.Get("email"),
		a.Get("mail"),
		a.Get("urn:oid:0.9.2342.19200300.100.1.3"),
		nameID, // fall back to NameID when it is an email address
	)
	displayName = firstNonEmpty(
		a.Get("displayName"),
		a.Get("cn"),
		a.Get("name"),
		a.Get("givenName")+" "+a.Get("sn"),
		email,
	)
	return email, displayName
}
