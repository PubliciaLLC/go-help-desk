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

    const alert = await screen.findByRole('alert')
    const text = alert.textContent ?? ''
    expect(text).toContain('Passkeys are unavailable')
    expect(text).toContain('BASE_URL')
    expect(text).toContain('localhost')
    expect(text).toContain('must be a domain and not an IP address')
  })

  it('says nothing when passkeys work', async () => {
    mockWarnings({ available: true, reason: '' })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing from a server that predates the field', async () => {
    mockWarnings(undefined)
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
