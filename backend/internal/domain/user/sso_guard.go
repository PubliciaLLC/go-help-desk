package user

import "fmt"

// ErrWouldOrphanInstance is the refusal to change an SSO setting that would
// leave every active administrator with no way to authenticate.
//
// The sibling of ErrLastAdmin, for the door that guard does not watch: this
// removes an administrator's ABILITY to authenticate rather than the
// administrator, so the count of administrators never changes and every
// *UnlessLastAdmin statement passes. See #300.
var ErrWouldOrphanInstance = fmt.Errorf(
	"%w: this change would leave no administrator able to sign in", ErrValidation)

// StrandedAdmins reports which of the given active administrators would have
// no way to authenticate if SAML's reachability becomes samlReachable and
// OIDC's becomes oidcReachable. See #300.
//
// The last-administrator guard (ErrLastAdmin, and the *UnlessLastAdmin
// statements) refuses to disable, demote or delete the sole administrator,
// because each of those removes an administrator while leaving the count
// wrong. Turning off the identity provider a passwordless administrator
// depends on is the same class of mistake reached through a door that guard
// does not watch: it removes their ABILITY TO AUTHENTICATE while leaving the
// row, the role and the count untouched, so every existing check passes.
//
// A password is always a viable channel regardless of either provider's
// reachability — local login is never gated by SSO settings. A federated
// subject is only a viable channel while its own provider is reachable: an
// account with an OIDCSubject and no password has no way in the moment OIDC
// stops answering, whatever SAML's state is, because that subject was never
// bound to SAML.
//
// Deliberately does not consult MFA (TOTP, and passkeys where that lands): a
// second factor is never a way IN on its own — it gates a session that has
// already authenticated with a first factor (password, SAML or OIDC), so it
// cannot make an otherwise-stranded administrator un-stranded, and it cannot
// be what strands one either. See #300 for the reasoning this separates from.
func StrandedAdmins(admins []User, samlReachable, oidcReachable bool) []User {
	var stranded []User
	for _, u := range admins {
		hasPassword := u.PasswordHash != ""
		hasSAML := u.SAMLSubject != "" && samlReachable
		hasOIDC := u.OIDCSubject != "" && oidcReachable
		if !hasPassword && !hasSAML && !hasOIDC {
			stranded = append(stranded, u)
		}
	}
	return stranded
}
