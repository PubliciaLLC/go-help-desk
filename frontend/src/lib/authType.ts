// How an account signs in, as the admin pages word it. The server derives
// auth_type from the credentials the account holds: "local", "saml", "oidc",
// "both" (local + SAML), or a "+"-joined combination that includes oidc.
// Anything unrecognised reads as Local, which is what every unknown value did
// before OIDC was reported.
export function authTypeLabel(authType: string, detailed = false): string {
  if (authType === 'service') return 'Never signs in'
  const parts = authType === 'both' ? ['local', 'saml'] : authType.split('+')
  const local = parts.includes('local')
  const sso = parts.filter((p) => p === 'saml' || p === 'oidc')
  if (sso.length === 0) return 'Local'
  // The detail page names the protocol when it is the only way in; with a
  // password alongside, "Local + SSO" has always been enough.
  const ssoLabel = detailed && !local ? `SSO (${sso.map((p) => p.toUpperCase()).join(' + ')})` : 'SSO'
  return local ? `Local + ${ssoLabel}` : ssoLabel
}
