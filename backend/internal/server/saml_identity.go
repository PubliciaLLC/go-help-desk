package server

import (
	"github.com/crewjam/saml/samlsp"
)

// Claim names Microsoft Entra ID sends by default (no FriendlyName, so the
// SAML library keys them by these full URIs), and the LDAP mail OID.
const (
	samlOIDMail           = "urn:oid:0.9.2342.19200300.100.1.3"
	samlClaimEmail        = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
	samlClaimGivenName    = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname"
	samlClaimSurname      = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname"
	samlClaimName         = "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"
	samlClaimEntraDisplay = "http://schemas.microsoft.com/identity/claims/displayname"
)

// samlIdentity is the email and display name a SAML sign-in gives the account.
func samlIdentity(nameID string, a samlsp.Attributes) (email, displayName string) {
	email = firstNonEmpty(a.Get("email"), a.Get("mail"), a.Get(samlOIDMail), a.Get(samlClaimEmail), nameID)
	given := firstNonEmpty(a.Get("givenName"), a.Get("firstName"), a.Get(samlClaimGivenName))
	surname := firstNonEmpty(a.Get("sn"), a.Get("lastName"), a.Get(samlClaimSurname))
	displayName = firstNonEmpty(
		a.Get("displayName"), a.Get(samlClaimEntraDisplay), a.Get("cn"), a.Get("name"),
		given+" "+surname,
		// Entra fills this with the user principal name, not a person's
		// name, so it ranks below the given name and surname (#461).
		a.Get(samlClaimName),
		email,
	)
	return email, displayName
}
