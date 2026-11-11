import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #619: an expired IdP signing certificate fails every SAML sign-in, and
// metadata pasted as XML never refreshes itself. The banner is the only warning
// an administrator gets before that happens.

function mockWarnings(samlIdpCertificate?: { expires_at: string; expired: boolean }) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' },
          saml_idp_certificate: samlIdpCertificate,
        },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the SAML IdP certificate warning', () => {
  it('says when the certificate expires and what to do', async () => {
    const expiresAt = '2030-03-09T12:00:00Z'
    mockWarnings({ expires_at: expiresAt, expired: false })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('SAML sign-in certificate expires')
    expect(text).toContain(new Date(expiresAt).toLocaleDateString())
    expect(text).toContain('metadata URL')
    expect(text).toContain('paste')
  })

  it('says sign-in is failing once it has expired', async () => {
    mockWarnings({ expires_at: '2020-01-01T00:00:00Z', expired: true })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('SAML sign-in certificate has expired')
    expect(text).toContain('SAML sign-in is failing')
  })

  it('says nothing when the server reports no problem', async () => {
    mockWarnings(undefined)
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })
})
