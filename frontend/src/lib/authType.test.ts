import { describe, expect, it } from 'vitest'
import { authTypeLabel } from './authType'

describe('authTypeLabel', () => {
  it.each([
    ['local', 'Local', 'Local'],
    ['saml', 'SSO', 'SSO (SAML)'],
    ['oidc', 'SSO', 'SSO (OIDC)'],
    ['both', 'Local + SSO', 'Local + SSO'],
    ['local+oidc', 'Local + SSO', 'Local + SSO'],
    ['saml+oidc', 'SSO', 'SSO (SAML + OIDC)'],
    ['local+saml+oidc', 'Local + SSO', 'Local + SSO'],
  ])('%s reads %s in the list and %s on the detail page', (type, short, long) => {
    expect(authTypeLabel(type)).toBe(short)
    expect(authTypeLabel(type, true)).toBe(long)
  })

  it('reads a value it does not know as Local, as before', () => {
    expect(authTypeLabel('passkey-only-future-thing')).toBe('Local')
  })
})
