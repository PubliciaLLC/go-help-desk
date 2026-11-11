import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #619: an expired IdP signing certificate fails every SAML sign-in, and
// metadata pasted as XML never refreshes itself. The banner is the only warning
// an administrator gets before that happens.

function mockWarnings(
  samlIdpCertificate?: { expires_at: string; expired: boolean },
  samlMetadataRefresh?: { problem: string },
) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' },
          saml_idp_certificate: samlIdpCertificate,
          saml_metadata_refresh: samlMetadataRefresh,
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

// The hourly metadata refresh may not move the trust anchor unattended
// (#619): it declines, and an administrator is told why.
describe('the SAML metadata refresh warning', () => {
  it('tells an administrator an http metadata URL is never refreshed', async () => {
    mockWarnings(undefined, { problem: 'http_url' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('SAML metadata is not refreshed automatically')
    expect(text).toContain('https')
  })

  it('says the IdP changed its signing certificate and an administrator must re-save', async () => {
    mockWarnings(undefined, { problem: 'certificate_changed' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('signing certificate changed')
    expect(text).toContain('re-save')
    expect(text).toContain('was not applied')
  })

  it('says the IdP entity ID changed and was not applied', async () => {
    mockWarnings(undefined, { problem: 'entity_id_changed' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('entity ID changed')
    expect(text).toContain('re-save')
  })
})
