import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #494: an administrator whose BASE_URL is an IP address or a single-label
// host has no passkeys, and the instance used to refuse to start instead of
// saying so. Now it starts, and this banner is where they learn why.

function mockWarnings(passkeys: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' },
          passkeys,
        },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the passkeys warning', () => {
  it('tells an administrator that BASE_URL must be a domain name', async () => {
    mockWarnings({ available: false, reason: 'must be a domain and not an IP address' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('region', { name: 'Server notices' })
    const text = alert.textContent ?? ''
    expect(text).toContain('Passkeys are unavailable')
    expect(text).toContain('BASE_URL')
    expect(text).toContain('localhost')
    expect(text).toContain('must be a domain and not an IP address')
  })

  // The consequence people most need to know: a passkey-only account is
  // refused, not let in on its password. The sentence says when it applies and
  // how the last administrator, who cannot ask another one, gets back in.
  it('says who cannot sign in, when, and how to recover', async () => {
    mockWarnings({ available: false, reason: 'must be a domain and not an IP address' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('anyone whose only second factor is a passkey cannot sign in')
    expect(text).toContain('while sign-in MFA is on')
    expect(text).toContain('until an administrator resets their sign-in factors')
    expect(text).toContain('docker compose exec app /app/server reset-factors <email>')
    // The wording this replaced promised a fallback that no longer exists.
    expect(text).not.toContain('sign in with their password and an authenticator app')
  })

  it('says nothing when passkeys work', async () => {
    mockWarnings({ available: true, reason: '' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  it('says nothing from a server that predates the field', async () => {
    mockWarnings(undefined)
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })
})

// #616: a passkey requirement that is not being applied is a security control
// that is off, and the administrator is told which roles it is off for.
describe('the passkey requirement suspended warning', () => {
  it('names the roles and says authenticator codes are accepted until BASE_URL is fixed', async () => {
    mockWarnings({ available: false, reason: 'must be a domain', suspended_roles: ['staff', 'admin'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('Passkey requirement suspended')
    expect(text).toContain('staff, admin')
    expect(text).toContain('authenticator app code')
    expect(text).toContain('BASE_URL')
  })

  it('has no such entry when nothing is suspended', async () => {
    mockWarnings({ available: false, reason: 'must be a domain', suspended_roles: [] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).toContain('Passkeys are unavailable')
    expect(text).not.toContain('Passkey requirement suspended')
  })

  it('has no such entry from a server that predates the field', async () => {
    mockWarnings({ available: false, reason: 'must be a domain' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('region', { name: 'Server notices' })).textContent ?? ''
    expect(text).not.toContain('Passkey requirement suspended')
  })
})
