package auth

import "slices"

// What an identity provider's assertion may say about how the user
// authenticated, and what that is worth to us. Every accepted value lives in
// this file so the "second factor proved" check and the "passkey-grade" check
// read one table.
//
// Documented for Entra ID only. Microsoft documents `fido` and `hwk` as values
// of the `amr` claim (OIDC), and in SAML the authnmethodsreferences claim
// carries the same method values alongside
// http://schemas.microsoft.com/claims/multipleauthn when MFA was performed.
// See "Microsoft Entra ID claims: amr" in the Microsoft identity platform
// documentation (access tokens and ID tokens reference).
//
// Okta and Google Workspace are NOT listed: the values they send for a
// phishing-resistant sign-in are not confirmed, and none are invented here.
// A user of such a provider in a passkey-required role is not refused; the
// assertion simply is not passkey-grade, so they fall through to the local
// passkey step after SSO.

// samlAuthnMethodsAttr and samlMultipleAuthn are how Entra ID says, in a
// SAML assertion, that the user completed MFA: the attribute holds
// multipleauthn only then. For apps other than Salesforce the Entra
// administrator must add the `amr` optional claim with include_granular_amr,
// or the attribute is not sent and SSO sign-ins do not count as a proved
// factor.
const (
	samlAuthnMethodsAttr = "http://schemas.microsoft.com/claims/authnmethodsreferences"
	samlMultipleAuthn    = "http://schemas.microsoft.com/claims/multipleauthn"
)

// samlAuthnMethodsKeys are the keys the SAML library can file the attribute
// under: its Name, or its FriendlyName when the provider sets one. Entra sends
// no FriendlyName for this claim; a provider that did would otherwise never
// count.
var samlAuthnMethodsKeys = []string{samlAuthnMethodsAttr, "authnmethodsreferences"}

// oidcPasskeyAMR is the amr values (RFC 8176) that mean the user signed in with
// a FIDO2 credential: fido (a FIDO authenticator) and hwk (proof of possession
// of a hardware-secured key). Entra, documented.
var oidcPasskeyAMR = []string{"fido", "hwk"}

// samlPasskeyMethods are the same two values as they appear in the SAML
// authnmethodsreferences attribute. They count only together with
// samlMultipleAuthn.
var samlPasskeyMethods = []string{"fido", "hwk"}

// SAMLAssertedMFA reports whether a SAML assertion's attributes say the user
// completed a second factor. Anything else fails closed.
func SAMLAssertedMFA(attrs map[string][]string) bool {
	return slices.Contains(samlAuthnMethods(attrs), samlMultipleAuthn)
}

// SAMLAssertedPasskey reports whether a SAML assertion's attributes say the
// user signed in with a passkey: a fido or hwk method together with the
// multipleauthn marker. Anything else fails closed.
func SAMLAssertedPasskey(attrs map[string][]string) bool {
	methods := samlAuthnMethods(attrs)
	return slices.Contains(methods, samlMultipleAuthn) &&
		slices.ContainsFunc(methods, func(m string) bool { return slices.Contains(samlPasskeyMethods, m) })
}

func samlAuthnMethods(attrs map[string][]string) []string {
	var out []string
	for _, key := range samlAuthnMethodsKeys {
		out = append(out, attrs[key]...)
	}
	return out
}

// AssertedPasskey reports whether the provider says the user signed in with a
// passkey: fido or hwk in amr. A provider that sends no amr fails closed.
func (c OIDCClaims) AssertedPasskey() bool {
	return slices.ContainsFunc(c.AMR, func(m string) bool { return slices.Contains(oidcPasskeyAMR, m) })
}
